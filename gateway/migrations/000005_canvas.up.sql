CREATE TABLE canvas_documents(project_id TEXT PRIMARY KEY REFERENCES projects(id), document_json TEXT NOT NULL);
CREATE TABLE canvas_commands(project_id TEXT NOT NULL REFERENCES projects(id),request_id TEXT NOT NULL,input_json TEXT NOT NULL,applied_revision INTEGER NOT NULL,effects_json TEXT NOT NULL,PRIMARY KEY(project_id,request_id));
CREATE TABLE canvas_references(project_id TEXT NOT NULL REFERENCES projects(id),owner TEXT NOT NULL,kind TEXT NOT NULL,object_id TEXT NOT NULL,PRIMARY KEY(project_id,owner,kind,object_id));
CREATE INDEX canvas_references_object ON canvas_references(kind,object_id);
-- Original metadata absent from the frozen five core tables lives in support state.
CREATE TABLE canvas_composition_state(composition_id TEXT PRIMARY KEY,revision INTEGER NOT NULL DEFAULT 0);
INSERT INTO canvas_composition_state SELECT id,0 FROM compositions;
CREATE TABLE canvas_version_state(version_id TEXT PRIMARY KEY,resource_id TEXT NOT NULL,ordinal INTEGER NOT NULL,UNIQUE(resource_id,ordinal));
INSERT INTO canvas_version_state SELECT id,resource_id,row_number() OVER(PARTITION BY resource_id ORDER BY created_at,id) FROM versions;
CREATE TABLE canvas_resource_state(resource_id TEXT PRIMARY KEY,current_version_id TEXT,next_ordinal INTEGER NOT NULL DEFAULT 1);
INSERT INTO canvas_resource_state SELECT r.id,(SELECT v.version_id FROM canvas_version_state v WHERE v.resource_id=r.id ORDER BY ordinal DESC LIMIT 1),coalesce((SELECT max(ordinal)+1 FROM canvas_version_state v WHERE v.resource_id=r.id),1) FROM resources r;
CREATE TRIGGER canvas_composition_insert AFTER INSERT ON compositions BEGIN INSERT INTO canvas_composition_state VALUES(NEW.id,0); END;
CREATE TRIGGER canvas_composition_update AFTER UPDATE ON compositions BEGIN UPDATE canvas_composition_state SET revision=revision+1 WHERE composition_id=NEW.id; END;
CREATE TRIGGER canvas_composition_delete AFTER DELETE ON compositions BEGIN DELETE FROM canvas_composition_state WHERE composition_id=OLD.id; END;
CREATE TRIGGER canvas_resource_insert AFTER INSERT ON resources BEGIN INSERT INTO canvas_resource_state(resource_id) VALUES(NEW.id); UPDATE canvas_composition_state SET revision=revision+1 WHERE composition_id=NEW.composition_id; END;
CREATE TRIGGER canvas_resource_update AFTER UPDATE ON resources BEGIN UPDATE canvas_composition_state SET revision=revision+1 WHERE composition_id IN (SELECT NEW.composition_id UNION SELECT composition_id FROM composition_members WHERE resource_id=NEW.id); END;
CREATE TRIGGER canvas_resource_delete AFTER DELETE ON resources BEGIN DELETE FROM canvas_resource_state WHERE resource_id=OLD.id; UPDATE canvas_composition_state SET revision=revision+1 WHERE composition_id=OLD.composition_id; END;
CREATE TRIGGER canvas_version_insert AFTER INSERT ON versions BEGIN
 INSERT INTO canvas_version_state SELECT NEW.id,NEW.resource_id,next_ordinal FROM canvas_resource_state WHERE resource_id=NEW.resource_id;
 UPDATE canvas_resource_state SET current_version_id=NEW.id,next_ordinal=next_ordinal+1 WHERE resource_id=NEW.resource_id;
 UPDATE canvas_composition_state SET revision=revision+1 WHERE composition_id IN (SELECT composition_id FROM resources WHERE id=NEW.resource_id UNION SELECT composition_id FROM composition_members WHERE resource_id=NEW.resource_id);
END;
CREATE TRIGGER canvas_version_delete AFTER DELETE ON versions BEGIN
 DELETE FROM canvas_version_state WHERE version_id=OLD.id;
 UPDATE canvas_resource_state SET current_version_id=(SELECT version_id FROM canvas_version_state WHERE resource_id=OLD.resource_id ORDER BY ordinal DESC LIMIT 1) WHERE resource_id=OLD.resource_id AND current_version_id=OLD.id;
 UPDATE canvas_composition_state SET revision=revision+1 WHERE composition_id IN (SELECT composition_id FROM resources WHERE id=OLD.resource_id UNION SELECT composition_id FROM composition_members WHERE resource_id=OLD.resource_id);
END;
CREATE TRIGGER canvas_member_insert AFTER INSERT ON composition_members BEGIN UPDATE canvas_composition_state SET revision=revision+1 WHERE composition_id=NEW.composition_id; END;
CREATE TRIGGER canvas_member_delete AFTER DELETE ON composition_members BEGIN UPDATE canvas_composition_state SET revision=revision+1 WHERE composition_id=OLD.composition_id; END;
CREATE TRIGGER canvas_member_update AFTER UPDATE ON composition_members BEGIN UPDATE canvas_composition_state SET revision=revision+1 WHERE composition_id IN (OLD.composition_id,NEW.composition_id); END;
CREATE TRIGGER canvas_asset_update AFTER UPDATE ON assets BEGIN UPDATE canvas_composition_state SET revision=revision+1 WHERE composition_id IN (SELECT r.composition_id FROM resources r JOIN versions v ON v.resource_id=r.id WHERE v.asset_id=NEW.id UNION SELECT m.composition_id FROM composition_members m JOIN versions v ON v.resource_id=m.resource_id WHERE v.asset_id=NEW.id); END;
CREATE TRIGGER canvas_type_update AFTER UPDATE ON composition_types BEGIN UPDATE canvas_composition_state SET revision=revision+1 WHERE composition_id IN (SELECT id FROM compositions WHERE type_id=NEW.id); END;

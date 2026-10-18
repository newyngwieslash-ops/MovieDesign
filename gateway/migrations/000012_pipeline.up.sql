CREATE TABLE pipelines (
 id TEXT PRIMARY KEY, project_id TEXT NOT NULL REFERENCES projects(id),
 revision INTEGER NOT NULL DEFAULT 0 CHECK(revision>=0),
 prompt_status TEXT NOT NULL DEFAULT 'pending' CHECK(prompt_status IN ('pending','ready')),
 resource_id TEXT UNIQUE REFERENCES resources(id)
);
CREATE TABLE storyboard_revisions (
 pipeline_id TEXT NOT NULL REFERENCES pipelines(id), revision INTEGER NOT NULL CHECK(revision>0),
 content_json TEXT NOT NULL CHECK(json_valid(content_json)), semantic_hash TEXT NOT NULL,
 document_version_id TEXT NOT NULL UNIQUE REFERENCES versions(id), created_at INTEGER NOT NULL,
 PRIMARY KEY(pipeline_id,revision)
);
CREATE TRIGGER storyboard_revision_immutable BEFORE UPDATE ON storyboard_revisions BEGIN SELECT RAISE(ABORT,'storyboard revisions are immutable'); END;
CREATE TRIGGER storyboard_revision_no_delete BEFORE DELETE ON storyboard_revisions BEGIN SELECT RAISE(ABORT,'storyboard revisions are immutable'); END;
CREATE TABLE approvals (
 id TEXT PRIMARY KEY, pipeline_id TEXT NOT NULL, revision INTEGER NOT NULL,
 gate TEXT NOT NULL, receipt_id TEXT NOT NULL REFERENCES question_receipts(id),
 valid INTEGER NOT NULL DEFAULT 1 CHECK(valid IN (0,1)),
 FOREIGN KEY(pipeline_id,revision) REFERENCES storyboard_revisions(pipeline_id,revision)
);
CREATE TABLE prompt_contracts (
 id TEXT PRIMARY KEY, pipeline_id TEXT NOT NULL, revision INTEGER NOT NULL,
 document_version_id TEXT NOT NULL REFERENCES versions(id), content_json TEXT NOT NULL CHECK(json_valid(content_json)),
 status TEXT NOT NULL CHECK(status IN ('pending','ready','submitted','invalidated')),
 FOREIGN KEY(pipeline_id,revision) REFERENCES storyboard_revisions(pipeline_id,revision)
);
CREATE TABLE version_dependencies (
 version_id TEXT NOT NULL REFERENCES versions(id), source_version_id TEXT NOT NULL REFERENCES versions(id),
 kind TEXT NOT NULL, PRIMARY KEY(version_id,source_version_id,kind), CHECK(version_id<>source_version_id)
);

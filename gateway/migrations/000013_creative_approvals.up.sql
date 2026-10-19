CREATE TABLE creative_artifacts (
 pipeline_id TEXT NOT NULL REFERENCES pipelines(id), gate TEXT NOT NULL CHECK(gate IN ('intent','script')),
 revision INTEGER NOT NULL CHECK(revision>0), document_version_id TEXT NOT NULL UNIQUE REFERENCES versions(id),
 resource_id TEXT NOT NULL REFERENCES resources(id), content TEXT NOT NULL, semantic_hash TEXT NOT NULL,
 source_version_id TEXT REFERENCES versions(id), created_at INTEGER NOT NULL,
 PRIMARY KEY(pipeline_id,gate,revision)
);
CREATE TRIGGER creative_artifact_immutable BEFORE UPDATE ON creative_artifacts BEGIN SELECT RAISE(ABORT,'creative artifacts are immutable'); END;
CREATE TRIGGER creative_artifact_no_delete BEFORE DELETE ON creative_artifacts BEGIN SELECT RAISE(ABORT,'creative artifacts are immutable'); END;
ALTER TABLE approvals RENAME TO storyboard_approvals_legacy;
CREATE TABLE approvals (
 id TEXT PRIMARY KEY, pipeline_id TEXT NOT NULL REFERENCES pipelines(id), revision INTEGER NOT NULL CHECK(revision>0),
 gate TEXT NOT NULL CHECK(gate IN ('intent','script','storyboard')), receipt_id TEXT NOT NULL UNIQUE REFERENCES question_receipts(id),
 document_version_id TEXT NOT NULL REFERENCES versions(id), semantic_hash TEXT NOT NULL,
 valid INTEGER NOT NULL DEFAULT 1 CHECK(valid IN (0,1))
);
INSERT INTO approvals SELECT a.id,a.pipeline_id,a.revision,a.gate,a.receipt_id,s.document_version_id,s.semantic_hash,0 FROM storyboard_approvals_legacy a JOIN storyboard_revisions s ON s.pipeline_id=a.pipeline_id AND s.revision=a.revision;
DROP TABLE storyboard_approvals_legacy;

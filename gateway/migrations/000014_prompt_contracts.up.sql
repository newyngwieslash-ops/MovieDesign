-- Support tables only; the five core tables are never altered.
CREATE TABLE prompt_revisions (
  pipeline_id TEXT NOT NULL REFERENCES pipelines(id),
  revision INTEGER NOT NULL CHECK(revision > 0),
  storyboard_revision INTEGER NOT NULL CHECK(storyboard_revision > 0),
  document_version_id TEXT NOT NULL UNIQUE REFERENCES versions(id),
  resource_id TEXT NOT NULL REFERENCES resources(id),
  content TEXT NOT NULL,
  prompt_sha256 TEXT NOT NULL,
  source_version_id TEXT NOT NULL REFERENCES versions(id),
  approval_id TEXT NOT NULL REFERENCES approvals(id),
  created_at INTEGER NOT NULL,
  PRIMARY KEY(pipeline_id, revision),
  FOREIGN KEY(pipeline_id, storyboard_revision) REFERENCES storyboard_revisions(pipeline_id, revision)
);
CREATE TRIGGER prompt_revision_immutable BEFORE UPDATE ON prompt_revisions BEGIN SELECT RAISE(ABORT, 'prompt revisions are immutable'); END;
CREATE TRIGGER prompt_revision_no_delete BEFORE DELETE ON prompt_revisions BEGIN SELECT RAISE(ABORT, 'prompt revisions are immutable'); END;

ALTER TABLE prompt_contracts ADD COLUMN prompt_revision INTEGER NOT NULL DEFAULT 0;
ALTER TABLE prompt_contracts ADD COLUMN prompt_sha256 TEXT NOT NULL DEFAULT '';
ALTER TABLE prompt_contracts ADD COLUMN approval_id TEXT REFERENCES approvals(id);
ALTER TABLE prompt_contracts ADD COLUMN task_id TEXT REFERENCES tasks(id);
ALTER TABLE prompt_contracts ADD COLUMN created_at INTEGER NOT NULL DEFAULT 0;

CREATE INDEX idx_prompt_contracts_pipeline_status ON prompt_contracts(pipeline_id, status, revision);
CREATE INDEX idx_prompt_contracts_version ON prompt_contracts(document_version_id);
CREATE INDEX idx_prompt_contracts_task ON prompt_contracts(task_id);

CREATE TRIGGER prompt_contract_no_delete
BEFORE DELETE ON prompt_contracts
BEGIN
  SELECT RAISE(ABORT, 'prompt contracts are immutable history');
END;

CREATE TRIGGER prompt_contract_immutable
BEFORE UPDATE ON prompt_contracts
WHEN OLD.id != NEW.id
  OR OLD.pipeline_id != NEW.pipeline_id
  OR OLD.revision != NEW.revision
  OR OLD.document_version_id != NEW.document_version_id
  OR OLD.prompt_revision != NEW.prompt_revision
  OR (OLD.status IN ('ready', 'submitted') AND (
    OLD.content_json != NEW.content_json
    OR OLD.prompt_sha256 != NEW.prompt_sha256
    OR COALESCE(OLD.approval_id, '') != COALESCE(NEW.approval_id, '')
  ))
  OR (OLD.task_id IS NOT NULL AND OLD.task_id != '' AND COALESCE(NEW.task_id, '') != OLD.task_id)
  OR (OLD.status = 'submitted' AND NEW.status != 'submitted')
  OR (OLD.status = 'invalidated' AND NEW.status != 'invalidated')
  OR NEW.status NOT IN ('pending', 'ready', 'submitted', 'invalidated')
BEGIN
  SELECT RAISE(ABORT, 'prompt contract payload and submitted status are immutable');
END;

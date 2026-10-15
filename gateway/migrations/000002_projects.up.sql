-- ADR-M1: Composition is the unit of independent identity, decisions and reuse.
-- Revisions of the same content keep Resource identity and add a Version.
-- Independent content gets a new Resource; derivation does not imply identity.
-- Co-occurrence does not merge objects; member roles describe use, not identity.
CREATE TABLE compositions (
  id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL,
  type_id TEXT NOT NULL,
  name TEXT NOT NULL,
  attributes TEXT
);
CREATE TABLE resources (
  id TEXT PRIMARY KEY,
  composition_id TEXT NOT NULL,
  description TEXT
);
CREATE TABLE versions (
  id TEXT PRIMARY KEY,
  resource_id TEXT NOT NULL,
  asset_id TEXT REFERENCES assets(id),
  source_version_id TEXT,
  origin TEXT NOT NULL DEFAULT 'agent',
  created_at INTEGER NOT NULL
);
CREATE TABLE composition_members (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  composition_id TEXT NOT NULL,
  resource_id TEXT NOT NULL,
  role TEXT NOT NULL
);

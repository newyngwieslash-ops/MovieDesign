-- ADR-M1: Keep the supplied asset contract unchanged; hashes are populated in M2.
CREATE TABLE assets (
  id TEXT PRIMARY KEY,
  path TEXT NOT NULL UNIQUE,
  status TEXT NOT NULL DEFAULT 'active',
  size INTEGER NOT NULL, mtime_ms INTEGER NOT NULL,
  dev_id INTEGER, inode INTEGER,
  quick_hash TEXT,
  full_hash TEXT,
  width INTEGER, height INTEGER, duration_ms INTEGER, mime_type TEXT,
  name TEXT NOT NULL, metadata TEXT,
  created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
);

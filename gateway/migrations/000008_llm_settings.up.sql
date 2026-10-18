CREATE TABLE llm_settings (
 id INTEGER PRIMARY KEY CHECK(id=1),
 config_json TEXT NOT NULL,
 credential_version TEXT NOT NULL DEFAULT ''
);

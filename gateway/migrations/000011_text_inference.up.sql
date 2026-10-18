CREATE TABLE text_requests (
 id TEXT PRIMARY KEY, task_id TEXT NOT NULL, attempt INTEGER NOT NULL,
 native_session_id TEXT UNIQUE, native_message_id TEXT NOT NULL,
 state TEXT NOT NULL, created_at INTEGER NOT NULL,
 FOREIGN KEY(task_id,attempt) REFERENCES task_attempts(task_id,attempt),
 UNIQUE(task_id,attempt)
);
CREATE TABLE text_inference_requests (
 turn_id TEXT NOT NULL REFERENCES text_requests(id), step INTEGER NOT NULL,
 fingerprint TEXT NOT NULL, state TEXT NOT NULL, tool_ids_json TEXT NOT NULL DEFAULT '[]',
 result_ids_json TEXT NOT NULL DEFAULT '[]', created_at INTEGER NOT NULL,
 PRIMARY KEY(turn_id,step), UNIQUE(turn_id,fingerprint)
);

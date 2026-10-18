ALTER TABLE chat_sessions ADD COLUMN title TEXT NOT NULL DEFAULT '';
ALTER TABLE chat_sessions ADD COLUMN state TEXT NOT NULL DEFAULT 'ready';
CREATE TABLE chat_dispatch (
 message_id TEXT PRIMARY KEY REFERENCES chat_messages(id), native_message_id TEXT NOT NULL UNIQUE,
 turn_id TEXT REFERENCES chat_turns(id), state TEXT NOT NULL, terminal INTEGER NOT NULL DEFAULT 0,
 error TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL
);
CREATE TABLE chat_parts (
 session_id TEXT NOT NULL REFERENCES chat_sessions(id), message_id TEXT NOT NULL, part_id TEXT NOT NULL,
 snapshot_json TEXT NOT NULL, public_json TEXT NOT NULL, PRIMARY KEY(session_id,message_id,part_id)
);
CREATE TABLE native_questions (
 native_id TEXT PRIMARY KEY, question_id TEXT UNIQUE REFERENCES questions(id),
 turn_id TEXT NOT NULL REFERENCES chat_turns(id), native_json TEXT NOT NULL,
 send_state TEXT NOT NULL DEFAULT 'pending', receipt_id TEXT REFERENCES question_receipts(id)
);
CREATE TABLE inference_requests (
 turn_id TEXT NOT NULL REFERENCES chat_turns(id), step INTEGER NOT NULL,
 fingerprint TEXT NOT NULL, state TEXT NOT NULL, tool_ids_json TEXT NOT NULL DEFAULT '[]',
 result_ids_json TEXT NOT NULL DEFAULT '[]', created_at INTEGER NOT NULL,
 PRIMARY KEY(turn_id,step), UNIQUE(turn_id,fingerprint)
);

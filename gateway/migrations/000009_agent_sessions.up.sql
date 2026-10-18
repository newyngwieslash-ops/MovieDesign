CREATE TABLE chat_sessions (
 id TEXT PRIMARY KEY, project_id TEXT NOT NULL REFERENCES projects(id), native_session_id TEXT UNIQUE,
 created_at INTEGER NOT NULL
);
CREATE TABLE chat_messages (
 id TEXT PRIMARY KEY, session_id TEXT NOT NULL REFERENCES chat_sessions(id), request_id TEXT NOT NULL,
 role TEXT NOT NULL CHECK(role IN ('user','assistant')), content_json TEXT NOT NULL, created_at INTEGER NOT NULL,
 UNIQUE(session_id,request_id)
);
CREATE TABLE chat_turns (
 id TEXT PRIMARY KEY, session_id TEXT NOT NULL REFERENCES chat_sessions(id), epoch TEXT NOT NULL UNIQUE,
 state TEXT NOT NULL CHECK(state IN ('active','closed','interrupted')), call_count INTEGER NOT NULL DEFAULT 0,
 created_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX agent_one_active_turn ON chat_turns((1)) WHERE state='active';
CREATE TABLE agent_tool_calls (
 turn_id TEXT NOT NULL REFERENCES chat_turns(id), call_id TEXT NOT NULL, tool TEXT NOT NULL,
 request_id TEXT NOT NULL DEFAULT '', input_json TEXT NOT NULL, accepted INTEGER NOT NULL,
 result_json TEXT, created_at INTEGER NOT NULL, PRIMARY KEY(turn_id,call_id)
);
CREATE TABLE project_rules (
 project_id TEXT PRIMARY KEY REFERENCES projects(id), content TEXT NOT NULL, receipt_id TEXT NOT NULL
);

CREATE TABLE inference_stream (
 scope TEXT NOT NULL CHECK(scope IN ('chat','text')), turn_id TEXT NOT NULL,
 step INTEGER NOT NULL CHECK(step>0), seq INTEGER NOT NULL CHECK(seq>0 AND seq<=65536),
 project_id TEXT NOT NULL, session_id TEXT NOT NULL,
 frame BLOB NOT NULL, text_delta TEXT NOT NULL, created_at INTEGER NOT NULL,
 PRIMARY KEY(scope,turn_id,step,seq)
);

DROP TABLE inference_requests;
DROP TABLE native_questions;
DROP TABLE chat_parts;
DROP TABLE chat_dispatch;
ALTER TABLE chat_sessions DROP COLUMN state;
ALTER TABLE chat_sessions DROP COLUMN title;

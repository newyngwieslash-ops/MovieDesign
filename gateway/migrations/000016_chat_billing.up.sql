ALTER TABLE chat_dispatch ADD COLUMN billing_question_id TEXT REFERENCES questions(id);
ALTER TABLE chat_dispatch ADD COLUMN billing_json TEXT;

-- +goose Up
CREATE TABLE ai_work (
 id INTEGER PRIMARY KEY,
 kind TEXT NOT NULL CHECK (kind IN ('fact_refill','meme_refill','feature_title')),
 scope_id INTEGER NOT NULL,
 work_key TEXT NOT NULL UNIQUE,
 state TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending','running','waiting','done')),
 priority INTEGER NOT NULL DEFAULT 0,
 next_attempt INTEGER NOT NULL DEFAULT 0,
 claimed_until INTEGER NOT NULL DEFAULT 0,
 attempts INTEGER NOT NULL DEFAULT 0,
 last_error_code TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL
);
CREATE INDEX ai_work_due ON ai_work(state,next_attempt,priority DESC,id);
CREATE INDEX ai_work_scope ON ai_work(kind,scope_id);

-- +goose Down
DROP INDEX ai_work_scope;
DROP INDEX ai_work_due;
DROP TABLE ai_work;

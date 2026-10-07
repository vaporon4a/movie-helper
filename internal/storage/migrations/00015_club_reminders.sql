-- +goose Up
CREATE TABLE club_reminder_settings (
 chat_id INTEGER PRIMARY KEY REFERENCES chats(chat_id),
 weekday INTEGER NOT NULL CHECK(weekday BETWEEN 0 AND 6),
 ordinal INTEGER NOT NULL CHECK(ordinal BETWEEN 1 AND 5),
 clock TEXT NOT NULL,
 enabled INTEGER NOT NULL DEFAULT 1,
 revision INTEGER NOT NULL DEFAULT 1,
 effective_at INTEGER NOT NULL
);
CREATE TABLE club_reminder_texts (
 id INTEGER PRIMARY KEY,
 chat_id INTEGER NOT NULL REFERENCES chats(chat_id),
 intent TEXT NOT NULL CHECK(intent IN ('explore','discuss')),
 tone TEXT NOT NULL,
 text TEXT NOT NULL,
 text_hash TEXT NOT NULL,
 provider TEXT NOT NULL,
 policy_version TEXT NOT NULL,
 created_at INTEGER NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('ready','reserved','used','expired')),
 UNIQUE(chat_id,text_hash)
);
CREATE TABLE club_reminder_deliveries (
 id INTEGER PRIMARY KEY,
 chat_id INTEGER NOT NULL REFERENCES chats(chat_id),
 revision INTEGER NOT NULL,
 cycle_date TEXT NOT NULL,
 intent TEXT NOT NULL,
 slot_at INTEGER NOT NULL,
 deadline INTEGER NOT NULL,
 next_attempt INTEGER NOT NULL DEFAULT 0,
 state TEXT NOT NULL CHECK(state IN ('ready','sending','sent','retry','unknown','failed','cancelled','expired')),
 text_id INTEGER REFERENCES club_reminder_texts(id),
 text_snapshot TEXT NOT NULL DEFAULT '',
 tone TEXT NOT NULL DEFAULT '',
 provider TEXT NOT NULL DEFAULT '',
 message_id INTEGER NOT NULL DEFAULT 0,
 error_code TEXT NOT NULL DEFAULT '',
 UNIQUE(chat_id,revision,cycle_date,intent)
);
CREATE UNIQUE INDEX club_reminder_live_cycle ON club_reminder_deliveries(chat_id,cycle_date,intent) WHERE state IN ('ready','retry','sending','sent','unknown');
CREATE INDEX club_reminder_due ON club_reminder_deliveries(state,next_attempt);
CREATE TABLE ai_work_new (
 id INTEGER PRIMARY KEY,
 kind TEXT NOT NULL CHECK(kind IN ('fact_refill','meme_refill','feature_title','club_reminder_refill')),
 scope_id INTEGER NOT NULL,
 work_key TEXT NOT NULL UNIQUE,
 state TEXT NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','running','waiting','done')),
 priority INTEGER NOT NULL DEFAULT 0,
 next_attempt INTEGER NOT NULL DEFAULT 0,
 claimed_until INTEGER NOT NULL DEFAULT 0,
 attempts INTEGER NOT NULL DEFAULT 0,
 last_error_code TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL
);
INSERT INTO ai_work_new SELECT * FROM ai_work;
DROP TABLE ai_work;
ALTER TABLE ai_work_new RENAME TO ai_work;
CREATE INDEX ai_work_due ON ai_work(state,next_attempt,priority DESC,id);
CREATE INDEX ai_work_scope ON ai_work(kind,scope_id);

-- +goose Down
DELETE FROM ai_work WHERE kind='club_reminder_refill';
DROP TABLE club_reminder_deliveries;
DROP TABLE club_reminder_texts;
DROP TABLE club_reminder_settings;

CREATE TABLE ai_work_old (
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
INSERT INTO ai_work_old SELECT * FROM ai_work;
DROP TABLE ai_work;
ALTER TABLE ai_work_old RENAME TO ai_work;
CREATE INDEX ai_work_due ON ai_work(state,next_attempt,priority DESC,id);
CREATE INDEX ai_work_scope ON ai_work(kind,scope_id);

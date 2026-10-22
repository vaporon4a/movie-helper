-- +goose Up
CREATE TABLE film_research_settings(chat_id INTEGER PRIMARY KEY, mode TEXT NOT NULL CHECK(mode IN ('off','shadow','on')));
CREATE TABLE film_research_jobs(
 session_id INTEGER PRIMARY KEY REFERENCES club_review_sessions(id),
 chat_id INTEGER NOT NULL,
 watch_id INTEGER NOT NULL,
 watch_revision INTEGER NOT NULL,
 mode TEXT NOT NULL CHECK(mode IN ('shadow','on')),
 stage TEXT NOT NULL DEFAULT 'sources' CHECK(stage IN ('sources','generate','audit','ready','rejected','superseded','frozen','fallback')),
 version INTEGER NOT NULL DEFAULT 1,
 repairs INTEGER NOT NULL DEFAULT 0 CHECK(repairs BETWEEN 0 AND 1),
 bundle_json TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(bundle_json)),
 audit_json TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(audit_json)),
 preparation_id TEXT REFERENCES film_research_preparations(id),
 updated_at INTEGER NOT NULL,
 FOREIGN KEY(watch_id,chat_id) REFERENCES club_watches(id,chat_id),
 FOREIGN KEY(watch_id,watch_revision) REFERENCES club_watch_audit(watch_id,revision)
);
CREATE TABLE film_research_approvals(
 preparation_id TEXT PRIMARY KEY REFERENCES film_research_preparations(id),
 audit_json TEXT NOT NULL CHECK(json_valid(audit_json)),
 created_at INTEGER NOT NULL
);
-- +goose StatementBegin
CREATE TRIGGER film_research_approval_immutable BEFORE UPDATE ON film_research_approvals
BEGIN SELECT RAISE(ABORT,'immutable film research approval'); END;
-- +goose StatementEnd
CREATE TABLE ai_work_new (
 id INTEGER PRIMARY KEY,
 kind TEXT NOT NULL CHECK(kind IN ('fact_refill','meme_refill','feature_title','club_reminder_refill','film_research')),
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
DROP TABLE film_research_approvals;
DROP TABLE film_research_jobs;
DROP TABLE film_research_settings;
DELETE FROM ai_work WHERE kind='film_research';
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


-- +goose Up
CREATE TABLE club_profile_settings (
 chat_id INTEGER PRIMARY KEY REFERENCES chats(chat_id),
 auto_review INTEGER NOT NULL DEFAULT 1 CHECK(auto_review IN (0,1)),
 expanded_topics INTEGER NOT NULL DEFAULT 0 CHECK(expanded_topics IN (0,1)),
 projection_from INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE club_profile_migration (version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL);
CREATE TABLE club_films (
 tmdb_id INTEGER PRIMARY KEY CHECK(tmdb_id>0),
 title TEXT NOT NULL,
 original_title TEXT NOT NULL DEFAULT '',
 release_year INTEGER,
 metadata_json TEXT NOT NULL DEFAULT '{}',
 source TEXT NOT NULL,
 fetched_at INTEGER NOT NULL
);
CREATE TABLE club_watches (
 id INTEGER PRIMARY KEY,
 chat_id INTEGER NOT NULL REFERENCES chats(chat_id),
 tmdb_id INTEGER NOT NULL REFERENCES club_films(tmdb_id),
 watched_date TEXT,
 date_precision TEXT NOT NULL CHECK(date_precision IN ('day','month','year','unknown')),
 zone TEXT NOT NULL,
 mode TEXT NOT NULL CHECK(mode IN ('fresh','retrospective')),
 state TEXT NOT NULL DEFAULT 'active' CHECK(state IN ('active','annulled')),
 origin TEXT NOT NULL,
 revision INTEGER NOT NULL DEFAULT 1 CHECK(revision>0),
 created_at INTEGER NOT NULL,
 UNIQUE(chat_id,origin), UNIQUE(id,chat_id)
);
CREATE UNIQUE INDEX club_watch_exact ON club_watches(chat_id,tmdb_id,watched_date)
 WHERE date_precision='day' AND state='active';
CREATE TABLE club_watch_audit (
 watch_id INTEGER NOT NULL REFERENCES club_watches(id),
 revision INTEGER NOT NULL,
 snapshot_json TEXT NOT NULL,
 reason TEXT NOT NULL,
 changed_at INTEGER NOT NULL,
 PRIMARY KEY(watch_id,revision)
);
CREATE TABLE club_review_sessions (
 id INTEGER PRIMARY KEY,
 chat_id INTEGER NOT NULL REFERENCES chats(chat_id),
 watch_id INTEGER NOT NULL UNIQUE,
 plan_json TEXT NOT NULL,
 coverage_json TEXT NOT NULL DEFAULT '{}',
 state TEXT NOT NULL CHECK(state IN ('queued','researching','completed_sufficient','partial','expired','cancelled','blocked')),
 reason TEXT NOT NULL DEFAULT '',
 start_before INTEGER NOT NULL,
 finish_before INTEGER NOT NULL,
 created_at INTEGER NOT NULL,
 UNIQUE(id,chat_id),
 FOREIGN KEY(watch_id,chat_id) REFERENCES club_watches(id,chat_id)
);
CREATE UNIQUE INDEX club_review_active ON club_review_sessions(chat_id) WHERE state='researching';
CREATE TABLE club_profile_schedules (
 id INTEGER PRIMARY KEY,
 chat_id INTEGER NOT NULL REFERENCES chats(chat_id),
 topic TEXT NOT NULL CHECK(topic IN ('genre','reference')),
 weekday INTEGER NOT NULL CHECK(weekday BETWEEN 0 AND 6),
 clock TEXT NOT NULL,
 enabled INTEGER NOT NULL CHECK(enabled IN (0,1)),
 effective INTEGER NOT NULL,
 UNIQUE(chat_id,topic,weekday), UNIQUE(id,chat_id)
);
CREATE TABLE club_slot_roles (
 chat_id INTEGER NOT NULL REFERENCES chats(chat_id),
 week TEXT NOT NULL,
 schedule_id INTEGER NOT NULL,
 role TEXT NOT NULL CHECK(role IN ('ordinary','research')),
 PRIMARY KEY(chat_id,week,schedule_id),
 FOREIGN KEY(schedule_id,chat_id) REFERENCES club_profile_schedules(id,chat_id)
);
CREATE TABLE club_poll_batches (
 id INTEGER PRIMARY KEY,
 chat_id INTEGER NOT NULL REFERENCES chats(chat_id),
 session_id INTEGER,
 occurrence TEXT NOT NULL,
 slot_at INTEGER NOT NULL,
 UNIQUE(chat_id,occurrence), UNIQUE(id,chat_id),
 FOREIGN KEY(session_id,chat_id) REFERENCES club_review_sessions(id,chat_id)
);
CREATE TABLE club_profile_polls (
 id INTEGER PRIMARY KEY,
 chat_id INTEGER NOT NULL REFERENCES chats(chat_id),
 topic TEXT NOT NULL CHECK(topic IN ('genre','reference','post_watch')),
 session_id INTEGER,
 batch_id INTEGER,
 position INTEGER NOT NULL DEFAULT 0 CHECK(position BETWEEN 0 AND 1),
 legacy_id INTEGER UNIQUE REFERENCES movie_rounds(id),
 occurrence TEXT NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('planned','creating','open','closing','completed','unknown','cancelled','failed')),
 question_json TEXT NOT NULL,
 telegram_poll_id TEXT NOT NULL DEFAULT '',
 message_id INTEGER NOT NULL DEFAULT 0,
 slot_at INTEGER NOT NULL,
 opened_at INTEGER NOT NULL DEFAULT 0,
 closes_at INTEGER NOT NULL,
 duration INTEGER NOT NULL CHECK(duration>0),
 next_attempt INTEGER NOT NULL DEFAULT 0,
 error_code TEXT NOT NULL DEFAULT '',
 result_json TEXT,
 cleanup_eligible INTEGER NOT NULL DEFAULT 1 CHECK(cleanup_eligible IN (0,1)),
 delivery_counted INTEGER NOT NULL DEFAULT 0 CHECK(delivery_counted IN (0,1)),
 UNIQUE(chat_id,occurrence,position), UNIQUE(batch_id,position), UNIQUE(id,chat_id),
 FOREIGN KEY(session_id,chat_id) REFERENCES club_review_sessions(id,chat_id),
 FOREIGN KEY(batch_id,chat_id) REFERENCES club_poll_batches(id,chat_id)
);
CREATE UNIQUE INDEX club_poll_telegram ON club_profile_polls(telegram_poll_id) WHERE telegram_poll_id<>'';
CREATE INDEX club_poll_due ON club_profile_polls(state,next_attempt,id);
CREATE TABLE club_poll_cleanup (
 poll_id INTEGER PRIMARY KEY,
 chat_id INTEGER NOT NULL,
 message_id INTEGER NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('pending','deleted','blocked','expired')),
 next_attempt INTEGER NOT NULL,
 deadline INTEGER NOT NULL,
 attempts INTEGER NOT NULL DEFAULT 0,
 reason TEXT NOT NULL DEFAULT '',
 FOREIGN KEY(poll_id,chat_id) REFERENCES club_profile_polls(id,chat_id)
);
CREATE TABLE club_watch_drafts (
 id INTEGER PRIMARY KEY,
 chat_id INTEGER NOT NULL REFERENCES chats(chat_id),
 operation_id INTEGER NOT NULL UNIQUE,
 input_json TEXT NOT NULL,
 candidates_json TEXT NOT NULL DEFAULT '[]',
 state TEXT NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','choice','saved','expired','not_found')),
 reason TEXT NOT NULL DEFAULT '',
 watch_id INTEGER,
 expires_at INTEGER NOT NULL,
 next_attempt INTEGER NOT NULL,
 message_id INTEGER NOT NULL DEFAULT 0,
 version INTEGER NOT NULL DEFAULT 1,
 rendered INTEGER NOT NULL DEFAULT 0,
 FOREIGN KEY(watch_id,chat_id) REFERENCES club_watches(id,chat_id)
);
CREATE TABLE club_film_enrichment (
 tmdb_id INTEGER PRIMARY KEY REFERENCES club_films(tmdb_id),
 next_attempt INTEGER NOT NULL,
 attempts INTEGER NOT NULL DEFAULT 0,
 complete INTEGER NOT NULL DEFAULT 0 CHECK(complete IN (0,1))
);

-- +goose Down
DROP TABLE club_film_enrichment;
DROP TABLE club_watch_drafts;
DROP TABLE club_poll_cleanup;
DROP TABLE club_profile_polls;
DROP TABLE club_poll_batches;
DROP TABLE club_slot_roles;
DROP TABLE club_profile_schedules;
DROP TABLE club_review_sessions;
DROP TABLE club_watch_audit;
DROP TABLE club_watches;
DROP TABLE club_films;
DROP TABLE club_profile_migration;
DROP TABLE club_profile_settings;

-- +goose Up
CREATE TABLE club_statistics_settings (
 chat_id INTEGER PRIMARY KEY REFERENCES chats(chat_id),
 weekday INTEGER NOT NULL DEFAULT 5 CHECK(weekday BETWEEN 0 AND 6),
 clock TEXT NOT NULL DEFAULT '09:00',
 enabled INTEGER NOT NULL DEFAULT 0 CHECK(enabled IN (0,1)),
 revision INTEGER NOT NULL DEFAULT 0,
 effective INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE club_statistics_snapshots (
 id INTEGER PRIMARY KEY,
 chat_id INTEGER NOT NULL REFERENCES chats(chat_id),
 week_key TEXT NOT NULL,
 zone TEXT NOT NULL,
 period_start INTEGER NOT NULL,
 period_end INTEGER NOT NULL,
 as_of INTEGER NOT NULL,
 version TEXT NOT NULL,
 snapshot_json TEXT NOT NULL,
 body TEXT NOT NULL,
 UNIQUE(chat_id,week_key)
);
CREATE TABLE club_statistics_deliveries (
 id INTEGER PRIMARY KEY,
 chat_id INTEGER NOT NULL REFERENCES chats(chat_id),
 snapshot_id INTEGER NOT NULL UNIQUE REFERENCES club_statistics_snapshots(id),
 settings_revision INTEGER NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('pending','sending','sent','retry','skipped','failed','unknown','cancelled','expired')),
 deadline INTEGER NOT NULL,
 next_attempt INTEGER NOT NULL,
 message_id INTEGER NOT NULL DEFAULT 0,
 reason TEXT NOT NULL DEFAULT ''
);
CREATE INDEX club_statistics_due ON club_statistics_deliveries(state,next_attempt);
CREATE TABLE club_statistics_claims (
 chat_id INTEGER NOT NULL REFERENCES chats(chat_id),
 claim_id TEXT NOT NULL,
 snapshot_id INTEGER NOT NULL REFERENCES club_statistics_snapshots(id),
 PRIMARY KEY(chat_id,claim_id)
);
-- +goose Down
DROP TABLE club_statistics_claims;
DROP TABLE club_statistics_deliveries;
DROP TABLE club_statistics_snapshots;
DROP TABLE club_statistics_settings;

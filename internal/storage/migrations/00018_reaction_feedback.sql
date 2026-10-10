-- +goose Up
ALTER TABLE items ADD COLUMN ai_model TEXT NOT NULL DEFAULT '';
CREATE TABLE feedback_posts (
 id INTEGER PRIMARY KEY,
 chat_id INTEGER NOT NULL,
 message_id INTEGER NOT NULL CHECK(message_id>0),
 publication TEXT NOT NULL CHECK(publication IN ('scheduled','preview')),
 published_at INTEGER NOT NULL,
 kind TEXT NOT NULL CHECK(kind IN ('meme','fact')),
 item_id INTEGER NOT NULL DEFAULT 0,
 content_key TEXT NOT NULL,
 text TEXT NOT NULL,
 source TEXT NOT NULL,
 image TEXT NOT NULL,
 source_evidence TEXT NOT NULL,
 ai_provider TEXT NOT NULL,
 ai_model TEXT NOT NULL,
 generation_policy TEXT NOT NULL,
 counter_mode TEXT NOT NULL DEFAULT '',
 partial INTEGER NOT NULL DEFAULT 0 CHECK(partial IN (0,1)),
 last_event_at INTEGER NOT NULL DEFAULT 0,
 last_update_id INTEGER NOT NULL DEFAULT -1,
 snapshot_at INTEGER NOT NULL DEFAULT 0,
 snapshot_update_id INTEGER NOT NULL DEFAULT -1,
 UNIQUE(chat_id,message_id)
);
CREATE INDEX feedback_posts_chat ON feedback_posts(chat_id,id);
CREATE TABLE feedback_events (
 update_id INTEGER PRIMARY KEY,
 post_id INTEGER NOT NULL REFERENCES feedback_posts(id),
 mode TEXT NOT NULL CHECK(mode IN ('delta','snapshot')),
 occurred_at INTEGER NOT NULL,
 received_at INTEGER NOT NULL,
 before_json TEXT NOT NULL,
 after_json TEXT NOT NULL,
 applied INTEGER NOT NULL CHECK(applied IN (0,1))
);
CREATE INDEX feedback_events_post ON feedback_events(post_id,occurred_at,update_id);
-- Signed counts preserve out-of-order deltas and reveal incomplete streams.
CREATE TABLE feedback_counts (
 post_id INTEGER NOT NULL REFERENCES feedback_posts(id),
 reaction_type TEXT NOT NULL CHECK(reaction_type IN ('emoji','custom_emoji','paid')),
 reaction_value TEXT NOT NULL,
 count INTEGER NOT NULL,
 PRIMARY KEY(post_id,reaction_type,reaction_value)
);
-- +goose Down
DROP TABLE feedback_counts;
DROP TABLE feedback_events;
DROP TABLE feedback_posts;
ALTER TABLE items DROP COLUMN ai_model;

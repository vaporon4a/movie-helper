-- +goose Up
CREATE TABLE feature_requests (
 id INTEGER PRIMARY KEY,
 chat_id INTEGER NOT NULL REFERENCES chats(chat_id),
 author_id INTEGER NOT NULL,
 body TEXT NOT NULL,
 title TEXT NOT NULL DEFAULT '',
 body_hash TEXT NOT NULL,
 state TEXT NOT NULL DEFAULT 'active' CHECK (state IN ('active','backlog','implemented','removed')),
 created_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL,
 UNIQUE(chat_id,body_hash)
);
CREATE INDEX feature_request_state ON feature_requests(chat_id,state,id);
CREATE INDEX feature_request_author ON feature_requests(chat_id,author_id,created_at);

CREATE TABLE feature_vote_schedules (
 chat_id INTEGER PRIMARY KEY REFERENCES chats(chat_id),
 weekday INTEGER NOT NULL CHECK (weekday BETWEEN 0 AND 6),
 clock TEXT NOT NULL,
 enabled INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0,1)),
 effective INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE feature_rounds (
 id INTEGER PRIMARY KEY,
 chat_id INTEGER NOT NULL REFERENCES chats(chat_id),
 slot_at INTEGER NOT NULL,
 token TEXT NOT NULL UNIQUE,
 state TEXT NOT NULL CHECK (state IN ('planned','opening','open','closing','ready','publishing','published','cancelled','failed','unknown')),
 message_id INTEGER NOT NULL DEFAULT 0,
 opened_at INTEGER NOT NULL DEFAULT 0,
 closes_at INTEGER NOT NULL,
 next_attempt INTEGER NOT NULL DEFAULT 0,
 winner_id INTEGER REFERENCES feature_requests(id),
 parent_id INTEGER REFERENCES feature_rounds(id),
 runoff_id INTEGER REFERENCES feature_rounds(id),
 outcome TEXT NOT NULL DEFAULT '',
 error_code TEXT NOT NULL DEFAULT '',
 UNIQUE(chat_id,slot_at)
);
CREATE UNIQUE INDEX feature_round_active_chat ON feature_rounds(chat_id)
 WHERE state IN ('planned','opening','open','closing');
CREATE INDEX feature_round_state ON feature_rounds(state,next_attempt,id);

CREATE TABLE feature_round_options (
 round_id INTEGER NOT NULL REFERENCES feature_rounds(id) ON DELETE CASCADE,
 position INTEGER NOT NULL CHECK (position>=0),
 idea_id INTEGER NOT NULL REFERENCES feature_requests(id),
 title TEXT NOT NULL,
 body TEXT NOT NULL,
 votes INTEGER NOT NULL DEFAULT 0 CHECK (votes>=0),
 PRIMARY KEY(round_id,position),
 UNIQUE(round_id,idea_id)
);

CREATE TABLE feature_votes (
 round_id INTEGER NOT NULL REFERENCES feature_rounds(id) ON DELETE CASCADE,
 user_id INTEGER NOT NULL,
 idea_id INTEGER NOT NULL REFERENCES feature_requests(id),
 updated_at INTEGER NOT NULL,
 PRIMARY KEY(round_id,user_id)
);
CREATE INDEX feature_vote_tally ON feature_votes(round_id,idea_id);

-- +goose Down
DROP INDEX feature_vote_tally;
DROP TABLE feature_votes;
DROP TABLE feature_round_options;
DROP INDEX feature_round_state;
DROP INDEX feature_round_active_chat;
DROP TABLE feature_rounds;
DROP TABLE feature_vote_schedules;
DROP INDEX feature_request_author;
DROP INDEX feature_request_state;
DROP TABLE feature_requests;

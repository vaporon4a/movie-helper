-- +goose Up
CREATE TABLE movie_poll_schedules (
 id INTEGER PRIMARY KEY,
 chat_id INTEGER NOT NULL REFERENCES chats(chat_id),
 feature TEXT NOT NULL CHECK (feature IN ('genre','reference')),
 weekday INTEGER NOT NULL CHECK (weekday BETWEEN 0 AND 6),
 clock TEXT NOT NULL,
 enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0,1)),
 effective INTEGER NOT NULL,
 UNIQUE(chat_id,feature,weekday)
);

CREATE TABLE movie_rounds (
 id INTEGER PRIMARY KEY,
 chat_id INTEGER NOT NULL REFERENCES chats(chat_id),
 feature TEXT NOT NULL CHECK (feature IN ('genre','reference')),
 slot_at INTEGER NOT NULL,
 state TEXT NOT NULL CHECK (state IN ('planned','poll_creating','open','closing','selecting','ready','publishing','published','cancelled','failed','unknown')),
 telegram_poll_id TEXT NOT NULL DEFAULT '',
 poll_message_id INTEGER NOT NULL DEFAULT 0,
 opened_at INTEGER NOT NULL DEFAULT 0,
 closes_at INTEGER NOT NULL,
 winner TEXT NOT NULL DEFAULT '',
 result_text TEXT NOT NULL DEFAULT '',
 next_attempt INTEGER NOT NULL DEFAULT 0,
 error_code TEXT NOT NULL DEFAULT '',
 publish_stage INTEGER NOT NULL DEFAULT 0 CHECK (publish_stage BETWEEN 0 AND 2),
 page2_state TEXT NOT NULL DEFAULT 'none' CHECK (page2_state IN ('none','ready','sending','sent','unknown')),
 UNIQUE(chat_id,feature,slot_at)
);

CREATE UNIQUE INDEX movie_round_active_chat ON movie_rounds(chat_id)
 WHERE state IN ('planned','poll_creating','open','closing','selecting','ready','publishing','unknown');
CREATE INDEX movie_round_state ON movie_rounds(state,next_attempt,id);
CREATE UNIQUE INDEX movie_round_poll ON movie_rounds(telegram_poll_id) WHERE telegram_poll_id<>'';

CREATE TABLE movie_poll_options (
 round_id INTEGER NOT NULL REFERENCES movie_rounds(id) ON DELETE CASCADE,
 position INTEGER NOT NULL CHECK (position BETWEEN 0 AND 11),
 option_kind TEXT NOT NULL CHECK (option_kind IN ('genre','movie')),
 provider_id INTEGER NOT NULL,
 label TEXT NOT NULL,
 votes INTEGER NOT NULL DEFAULT 0 CHECK (votes>=0),
 PRIMARY KEY(round_id,position)
);

CREATE TABLE movie_recommendations (
 round_id INTEGER NOT NULL REFERENCES movie_rounds(id) ON DELETE CASCADE,
 page INTEGER NOT NULL CHECK (page IN (1,2)),
 position INTEGER NOT NULL CHECK (position BETWEEN 0 AND 9),
 relation TEXT NOT NULL CHECK (relation IN ('top','similar','director','screenwriter','book_author')),
 tmdb_id INTEGER NOT NULL,
 title TEXT NOT NULL,
 release_year INTEGER NOT NULL DEFAULT 0,
 overview TEXT NOT NULL DEFAULT '',
 poster_path TEXT NOT NULL DEFAULT '',
 rating REAL NOT NULL DEFAULT 0,
 vote_count INTEGER NOT NULL DEFAULT 0,
 popularity REAL NOT NULL DEFAULT 0,
 PRIMARY KEY(round_id,page,position),
 UNIQUE(round_id,tmdb_id)
);
CREATE INDEX movie_recommendation_history ON movie_recommendations(tmdb_id,round_id);

-- +goose Down
DROP INDEX movie_recommendation_history;
DROP TABLE movie_recommendations;
DROP TABLE movie_poll_options;
DROP INDEX movie_round_poll;
DROP INDEX movie_round_state;
DROP INDEX movie_round_active_chat;
DROP TABLE movie_rounds;
DROP TABLE movie_poll_schedules;

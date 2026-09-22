-- +goose Up
CREATE TABLE chats (
 chat_id INTEGER PRIMARY KEY,
 zone TEXT NOT NULL DEFAULT '',
 active INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0,1))
);
CREATE TABLE schedules (
 chat_id INTEGER NOT NULL REFERENCES chats(chat_id),
 kind TEXT NOT NULL CHECK (kind IN ('meme','fact')),
 clock TEXT NOT NULL,
 enabled INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0,1)),
 effective INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY(chat_id,kind)
);
CREATE TABLE items (
 id INTEGER PRIMARY KEY,
 chat_id INTEGER NOT NULL REFERENCES chats(chat_id),
 kind TEXT NOT NULL CHECK (kind IN ('meme','fact')),
 text TEXT NOT NULL DEFAULT '',
 source TEXT NOT NULL DEFAULT '',
 image TEXT NOT NULL DEFAULT '',
 content_key TEXT NOT NULL,
 author_id INTEGER NOT NULL,
 state TEXT NOT NULL CHECK (state IN ('pending','approved','reserved','sent','rejected','failed')),
 created_at INTEGER NOT NULL,
 approved_by INTEGER,
 approved_at INTEGER,
 UNIQUE(chat_id,kind,content_key)
);
CREATE INDEX items_queue ON items(chat_id,kind,state,id);
CREATE TABLE deliveries (
 id INTEGER PRIMARY KEY,
 chat_id INTEGER NOT NULL REFERENCES chats(chat_id),
 kind TEXT NOT NULL CHECK (kind IN ('meme','fact')),
 local_date TEXT NOT NULL,
 item_id INTEGER REFERENCES items(id),
 deadline INTEGER NOT NULL,
 next_attempt INTEGER NOT NULL DEFAULT 0,
 state TEXT NOT NULL CHECK (state IN ('preparing','ready','retry','sending','unknown','sent','skipped','failed','cancelled')),
 message_id INTEGER,
 UNIQUE(chat_id,kind,local_date)
);
CREATE TABLE operations (update_id INTEGER PRIMARY KEY);

-- +goose Down
DROP TABLE operations;
DROP TABLE deliveries;
DROP TABLE items;
DROP TABLE schedules;
DROP TABLE chats;

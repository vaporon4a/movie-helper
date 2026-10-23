-- +goose Up
CREATE TABLE schedule_dialogs(token TEXT PRIMARY KEY,chat_id INTEGER NOT NULL,panel_id INTEGER NOT NULL,body TEXT NOT NULL,expires_at INTEGER NOT NULL,UNIQUE(chat_id,panel_id));
-- +goose Down
DROP TABLE schedule_dialogs;

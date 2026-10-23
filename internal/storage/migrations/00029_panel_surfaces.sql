-- +goose Up
CREATE TABLE interaction_surfaces(chat_id INTEGER NOT NULL,requester INTEGER NOT NULL,purpose TEXT NOT NULL,token TEXT NOT NULL,state TEXT NOT NULL,message_id INTEGER NOT NULL DEFAULT -1,expires_at INTEGER NOT NULL,PRIMARY KEY(chat_id,requester,purpose));
-- +goose Down
DROP TABLE interaction_surfaces;

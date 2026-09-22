-- +goose Up
ALTER TABLE chats ADD COLUMN moderation INTEGER NOT NULL DEFAULT 0 CHECK (moderation IN (0,1));
ALTER TABLE items ADD COLUMN ai_approved INTEGER NOT NULL DEFAULT 0 CHECK (ai_approved IN (0,1));

-- +goose Down
ALTER TABLE items DROP COLUMN ai_approved;
ALTER TABLE chats DROP COLUMN moderation;

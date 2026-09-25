-- +goose Up
ALTER TABLE deliveries ADD COLUMN preparation_error TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE deliveries DROP COLUMN preparation_error;

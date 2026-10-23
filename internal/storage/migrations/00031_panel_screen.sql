-- +goose Up
ALTER TABLE interaction_panels ADD COLUMN screen TEXT NOT NULL DEFAULT '';
-- +goose Down
ALTER TABLE interaction_panels DROP COLUMN screen;

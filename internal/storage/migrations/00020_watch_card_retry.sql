-- +goose Up
ALTER TABLE club_watch_drafts ADD COLUMN card_next_attempt INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE club_watch_drafts DROP COLUMN card_next_attempt;

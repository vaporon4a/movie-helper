-- +goose Up
ALTER TABLE club_watch_drafts ADD COLUMN card_closed INTEGER NOT NULL DEFAULT 0 CHECK(card_closed IN (0,1));
UPDATE club_watch_drafts SET version=version+1 WHERE message_id>0;

-- +goose Down
ALTER TABLE club_watch_drafts DROP COLUMN card_closed;

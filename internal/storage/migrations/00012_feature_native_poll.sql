-- +goose Up
ALTER TABLE feature_rounds ADD COLUMN ballot_mode TEXT NOT NULL DEFAULT 'private'
 CHECK (ballot_mode IN ('private','native'));
ALTER TABLE feature_rounds ADD COLUMN telegram_poll_id TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX feature_round_poll_id ON feature_rounds(telegram_poll_id)
 WHERE telegram_poll_id<>'';

-- +goose Down
DROP INDEX feature_round_poll_id;
ALTER TABLE feature_rounds DROP COLUMN telegram_poll_id;
ALTER TABLE feature_rounds DROP COLUMN ballot_mode;

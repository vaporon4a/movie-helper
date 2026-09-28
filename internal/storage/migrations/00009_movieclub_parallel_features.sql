-- +goose Up
DROP INDEX movie_round_active_chat;
CREATE UNIQUE INDEX movie_round_active_chat_feature ON movie_rounds(chat_id, feature)
WHERE state IN ('planned','poll_creating','open','closing','selecting','ready','publishing','unknown');

-- +goose Down
DROP INDEX movie_round_active_chat_feature;
CREATE UNIQUE INDEX movie_round_active_chat ON movie_rounds(chat_id)
WHERE state IN ('planned','poll_creating','open','closing','selecting','ready','publishing','unknown');

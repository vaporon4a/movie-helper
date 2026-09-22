-- +goose Up
CREATE TABLE meme_rejections (
    scope TEXT NOT NULL,
    source_key TEXT NOT NULL,
    expires_at INTEGER NOT NULL,
    PRIMARY KEY (scope, source_key)
);
CREATE INDEX meme_rejections_expiry ON meme_rejections(expires_at);

-- +goose Down
DROP TABLE meme_rejections;

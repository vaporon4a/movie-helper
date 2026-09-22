-- +goose Up
CREATE TABLE api_usage (utc_date TEXT PRIMARY KEY, requests INTEGER NOT NULL);
-- +goose Down
DROP TABLE api_usage;

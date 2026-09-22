-- +goose Up
CREATE TABLE provider_api_usage (
    provider TEXT NOT NULL,
    utc_date TEXT NOT NULL,
    requests INTEGER NOT NULL,
    PRIMARY KEY(provider, utc_date)
);

-- +goose Down
DROP TABLE provider_api_usage;

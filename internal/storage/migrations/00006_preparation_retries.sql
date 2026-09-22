-- +goose Up
ALTER TABLE deliveries ADD COLUMN slot_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE deliveries ADD COLUMN fetch_attempts INTEGER NOT NULL DEFAULT 0;
ALTER TABLE deliveries ADD COLUMN fetch_claimed INTEGER NOT NULL DEFAULT 0 CHECK(fetch_claimed IN (0,1));
UPDATE deliveries SET slot_at=deadline-3600;

-- +goose Down
UPDATE deliveries SET state='skipped' WHERE state='preparing';
UPDATE deliveries SET deadline=MIN(deadline,slot_at+3600);
ALTER TABLE deliveries DROP COLUMN fetch_claimed;
ALTER TABLE deliveries DROP COLUMN fetch_attempts;
ALTER TABLE deliveries DROP COLUMN slot_at;

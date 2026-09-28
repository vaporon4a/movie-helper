-- +goose Up
ALTER TABLE items ADD COLUMN source_evidence TEXT NOT NULL DEFAULT '';
ALTER TABLE items ADD COLUMN ai_provider TEXT NOT NULL DEFAULT '';
ALTER TABLE items ADD COLUMN generation_policy TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE items DROP COLUMN generation_policy;
ALTER TABLE items DROP COLUMN ai_provider;
ALTER TABLE items DROP COLUMN source_evidence;

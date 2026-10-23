-- +goose Up
CREATE TABLE recovery_confirmations(token TEXT PRIMARY KEY,chat_id INTEGER NOT NULL,panel_id INTEGER NOT NULL,body TEXT NOT NULL,expires_at INTEGER NOT NULL);
CREATE TABLE recovery_versions(domain TEXT NOT NULL,object_id INTEGER NOT NULL,version INTEGER NOT NULL DEFAULT 1,PRIMARY KEY(domain,object_id));
-- +goose StatementBegin
CREATE TRIGGER recovery_daily_version AFTER UPDATE ON deliveries
BEGIN
 INSERT INTO recovery_versions(domain,object_id,version) VALUES('daily',NEW.id,1) ON CONFLICT(domain,object_id) DO UPDATE SET version=version+1;
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER recovery_club_version AFTER UPDATE ON club_reminder_deliveries
BEGIN
 INSERT INTO recovery_versions(domain,object_id,version) VALUES('club',NEW.id,1) ON CONFLICT(domain,object_id) DO UPDATE SET version=version+1;
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER recovery_movies_version AFTER UPDATE ON club_profile_polls
BEGIN
 INSERT INTO recovery_versions(domain,object_id,version) VALUES('movies',NEW.id,1) ON CONFLICT(domain,object_id) DO UPDATE SET version=version+1;
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER recovery_ideas_version AFTER UPDATE ON feature_rounds
BEGIN
 INSERT INTO recovery_versions(domain,object_id,version) VALUES('ideas',NEW.id,1) ON CONFLICT(domain,object_id) DO UPDATE SET version=version+1;
END;
-- +goose StatementEnd
-- +goose Down
DROP TRIGGER recovery_daily_version;
DROP TRIGGER recovery_club_version;
DROP TRIGGER recovery_movies_version;
DROP TRIGGER recovery_ideas_version;
DROP TABLE recovery_versions;
DROP TABLE recovery_confirmations;

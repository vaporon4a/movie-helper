-- +goose Up
CREATE TABLE club_config_revisions(chat_id INTEGER NOT NULL,domain TEXT NOT NULL,revision INTEGER NOT NULL DEFAULT 0,PRIMARY KEY(chat_id,domain));
-- +goose StatementBegin
CREATE TRIGGER config_movies_insert AFTER INSERT ON club_profile_schedules
BEGIN
 INSERT INTO club_config_revisions(chat_id,domain,revision) VALUES(NEW.chat_id,'movies',1) ON CONFLICT(chat_id,domain) DO UPDATE SET revision=revision+1;
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER config_movies_update AFTER UPDATE OF clock,enabled,effective ON club_profile_schedules
BEGIN
 INSERT INTO club_config_revisions(chat_id,domain,revision) VALUES(NEW.chat_id,'movies',1) ON CONFLICT(chat_id,domain) DO UPDATE SET revision=revision+1;
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER config_movies_delete AFTER DELETE ON club_profile_schedules
BEGIN
 INSERT INTO club_config_revisions(chat_id,domain,revision) VALUES(OLD.chat_id,'movies',1) ON CONFLICT(chat_id,domain) DO UPDATE SET revision=revision+1;
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER config_club_insert AFTER INSERT ON club_reminder_settings
BEGIN
 INSERT INTO club_config_revisions(chat_id,domain,revision) VALUES(NEW.chat_id,'club',1) ON CONFLICT(chat_id,domain) DO UPDATE SET revision=revision+1;
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER config_club_update AFTER UPDATE OF clock,weekday,ordinal,enabled,revision,effective_at ON club_reminder_settings
BEGIN
 INSERT INTO club_config_revisions(chat_id,domain,revision) VALUES(NEW.chat_id,'club',1) ON CONFLICT(chat_id,domain) DO UPDATE SET revision=revision+1;
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER config_club_delete AFTER DELETE ON club_reminder_settings
BEGIN
 INSERT INTO club_config_revisions(chat_id,domain,revision) VALUES(OLD.chat_id,'club',1) ON CONFLICT(chat_id,domain) DO UPDATE SET revision=revision+1;
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER config_ideas_insert AFTER INSERT ON feature_vote_schedules
BEGIN
 INSERT INTO club_config_revisions(chat_id,domain,revision) VALUES(NEW.chat_id,'ideas',1) ON CONFLICT(chat_id,domain) DO UPDATE SET revision=revision+1;
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER config_ideas_update AFTER UPDATE OF clock,weekday,enabled,effective ON feature_vote_schedules
BEGIN
 INSERT INTO club_config_revisions(chat_id,domain,revision) VALUES(NEW.chat_id,'ideas',1) ON CONFLICT(chat_id,domain) DO UPDATE SET revision=revision+1;
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER config_ideas_delete AFTER DELETE ON feature_vote_schedules
BEGIN
 INSERT INTO club_config_revisions(chat_id,domain,revision) VALUES(OLD.chat_id,'ideas',1) ON CONFLICT(chat_id,domain) DO UPDATE SET revision=revision+1;
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER config_chat AFTER UPDATE OF zone,active ON chats
BEGIN
 INSERT INTO club_config_revisions VALUES(NEW.chat_id,'movies',1),(NEW.chat_id,'club',1),(NEW.chat_id,'ideas',1) ON CONFLICT(chat_id,domain) DO UPDATE SET revision=revision+1;
END;
-- +goose StatementEnd
-- +goose Down
DROP TRIGGER config_movies_insert;
DROP TRIGGER config_movies_update;
DROP TRIGGER config_movies_delete;
DROP TRIGGER config_club_insert;
DROP TRIGGER config_club_update;
DROP TRIGGER config_club_delete;
DROP TRIGGER config_ideas_insert;
DROP TRIGGER config_ideas_update;
DROP TRIGGER config_ideas_delete;
DROP TRIGGER config_chat;
DROP TABLE club_config_revisions;

-- +goose Up
ALTER TABLE chats ADD COLUMN daily_revision INTEGER NOT NULL DEFAULT 0;
ALTER TABLE club_watch_drafts ADD COLUMN card_page TEXT NOT NULL DEFAULT 'watch';
ALTER TABLE club_watch_drafts ADD COLUMN card_page_id INTEGER NOT NULL DEFAULT 0;
ALTER TABLE club_watch_drafts ADD COLUMN card_page_revision INTEGER NOT NULL DEFAULT 0;
CREATE TABLE daily_menu_panels(chat_id INTEGER NOT NULL REFERENCES chats(chat_id),panel_id INTEGER NOT NULL,closed INTEGER NOT NULL DEFAULT 0,PRIMARY KEY(chat_id,panel_id));
CREATE TABLE daily_menu_intents (
 token TEXT PRIMARY KEY, chat_id INTEGER NOT NULL REFERENCES chats(chat_id), panel_id INTEGER NOT NULL,
 kind TEXT NOT NULL CHECK(kind IN ('meme','fact')), action TEXT NOT NULL,
 revision INTEGER NOT NULL, actor_id INTEGER NOT NULL DEFAULT 0, prompt_id INTEGER NOT NULL DEFAULT 0,
 clock TEXT NOT NULL DEFAULT '', state TEXT NOT NULL DEFAULT 'pending', expires_at INTEGER NOT NULL
);
CREATE INDEX daily_menu_panel ON daily_menu_intents(chat_id,panel_id);
CREATE TABLE daily_menu_previews (
 chat_id INTEGER NOT NULL REFERENCES chats(chat_id), kind TEXT NOT NULL,
 token TEXT NOT NULL, state TEXT NOT NULL, until_at INTEGER NOT NULL, message_id INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY(chat_id,kind)
);
-- +goose StatementBegin
CREATE TRIGGER daily_chat_revision AFTER UPDATE OF zone,active,moderation ON chats
BEGIN
 UPDATE chats SET daily_revision=daily_revision+1 WHERE chat_id=NEW.chat_id;
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER daily_schedule_revision AFTER UPDATE OF clock,enabled,effective ON schedules
BEGIN
 UPDATE chats SET daily_revision=daily_revision+1 WHERE chat_id=NEW.chat_id;
END;
-- +goose StatementEnd
UPDATE club_watch_drafts SET version=version+1 WHERE card_closed=0;
-- +goose StatementBegin
CREATE TRIGGER panel_research_stage AFTER UPDATE OF stage ON film_research_jobs
WHEN OLD.stage<>NEW.stage
BEGIN
 UPDATE club_watch_drafts SET version=version+1 WHERE chat_id=NEW.chat_id AND state='saved' AND card_closed=0;
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER panel_research_enroll AFTER INSERT ON film_research_jobs
BEGIN
 UPDATE club_watch_drafts SET version=version+1 WHERE chat_id=NEW.chat_id AND state='saved' AND card_closed=0;
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER panel_review_state AFTER UPDATE OF state,reason,coverage_json,plan_json ON club_review_sessions
WHEN OLD.state<>NEW.state OR OLD.reason<>NEW.reason OR OLD.coverage_json<>NEW.coverage_json OR OLD.plan_json<>NEW.plan_json
BEGIN
 UPDATE club_watch_drafts SET version=version+1 WHERE chat_id=NEW.chat_id AND state='saved' AND card_closed=0;
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER panel_review_enqueue AFTER INSERT ON club_review_sessions
BEGIN
 UPDATE club_watch_drafts SET version=version+1 WHERE chat_id=NEW.chat_id AND state='saved' AND card_closed=0;
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER panel_profile_schedule AFTER UPDATE OF enabled,clock,effective ON club_profile_schedules
WHEN OLD.enabled<>NEW.enabled OR OLD.clock<>NEW.clock OR OLD.effective<>NEW.effective
BEGIN
 UPDATE club_watch_drafts SET version=version+1 WHERE chat_id=NEW.chat_id AND state='saved' AND card_closed=0;
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER panel_profile_schedule_new AFTER INSERT ON club_profile_schedules
BEGIN
 UPDATE club_watch_drafts SET version=version+1 WHERE chat_id=NEW.chat_id AND state='saved' AND card_closed=0;
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER panel_profile_chat AFTER UPDATE OF zone,active ON chats
WHEN OLD.zone<>NEW.zone OR OLD.active<>NEW.active
BEGIN
 UPDATE club_watch_drafts SET version=version+1 WHERE chat_id=NEW.chat_id AND state='saved' AND card_closed=0;
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER panel_research_mode AFTER UPDATE OF mode ON film_research_settings
WHEN OLD.mode<>NEW.mode
BEGIN
 UPDATE club_watch_drafts SET version=version+1 WHERE chat_id=NEW.chat_id AND state='saved' AND card_closed=0;
END;
-- +goose StatementEnd
-- +goose Down
DROP TRIGGER panel_research_stage;
DROP TRIGGER panel_research_enroll;
DROP TRIGGER panel_review_state;
DROP TRIGGER panel_review_enqueue;
DROP TRIGGER panel_profile_schedule;
DROP TRIGGER panel_profile_schedule_new;
DROP TRIGGER panel_profile_chat;
DROP TRIGGER panel_research_mode;

DROP TRIGGER daily_schedule_revision;
DROP TRIGGER daily_chat_revision;
DROP TABLE daily_menu_previews;
DROP TABLE daily_menu_intents;
DROP TABLE daily_menu_panels;
ALTER TABLE chats DROP COLUMN daily_revision;
ALTER TABLE club_watch_drafts DROP COLUMN card_page;
ALTER TABLE club_watch_drafts DROP COLUMN card_page_id;
ALTER TABLE club_watch_drafts DROP COLUMN card_page_revision;

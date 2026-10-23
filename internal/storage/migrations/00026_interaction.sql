-- +goose Up
CREATE TABLE interaction_panels(chat_id INTEGER NOT NULL,panel_id INTEGER NOT NULL,closed INTEGER NOT NULL DEFAULT 0,PRIMARY KEY(chat_id,panel_id));
INSERT INTO interaction_panels SELECT * FROM daily_menu_panels;
DROP TABLE daily_menu_panels;
CREATE TABLE interaction_intents(token TEXT PRIMARY KEY,chat_id INTEGER NOT NULL REFERENCES chats(chat_id),panel_id INTEGER NOT NULL,kind TEXT NOT NULL,action TEXT NOT NULL,revision INTEGER NOT NULL,actor_id INTEGER NOT NULL DEFAULT 0,prompt_id INTEGER NOT NULL DEFAULT 0,clock TEXT NOT NULL DEFAULT '',state TEXT NOT NULL DEFAULT 'pending',expires_at INTEGER NOT NULL);
INSERT INTO interaction_intents SELECT * FROM daily_menu_intents;
DROP TABLE daily_menu_intents;
CREATE INDEX interaction_panel ON interaction_intents(chat_id,panel_id);
INSERT INTO interaction_panels(chat_id,panel_id,closed)
 SELECT chat_id,message_id,1 FROM club_watch_drafts WHERE card_closed=1 AND message_id>0
 ON CONFLICT(chat_id,panel_id) DO UPDATE SET closed=1;
UPDATE club_watch_drafts SET card_closed=1 WHERE EXISTS(SELECT 1 FROM interaction_panels p WHERE p.chat_id=club_watch_drafts.chat_id AND p.panel_id=club_watch_drafts.message_id AND p.closed=1);
CREATE TABLE interaction_cleanup(chat_id INTEGER NOT NULL,message_id INTEGER NOT NULL,next_at INTEGER NOT NULL DEFAULT 0,attempts INTEGER NOT NULL DEFAULT 0,PRIMARY KEY(chat_id,message_id));
-- +goose StatementBegin
CREATE TRIGGER interaction_close_watch AFTER INSERT ON interaction_panels WHEN NEW.closed=1
BEGIN
 UPDATE club_watch_drafts SET card_closed=1 WHERE chat_id=NEW.chat_id AND message_id=NEW.panel_id;
 UPDATE interaction_intents SET state='cancelled',actor_id=0 WHERE chat_id=NEW.chat_id AND panel_id=NEW.panel_id AND state='pending';
 INSERT INTO interaction_cleanup(chat_id,message_id) VALUES(NEW.chat_id,NEW.panel_id) ON CONFLICT DO NOTHING;
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER interaction_update_watch AFTER UPDATE OF closed ON interaction_panels WHEN NEW.closed=1
BEGIN
 UPDATE club_watch_drafts SET card_closed=1 WHERE chat_id=NEW.chat_id AND message_id=NEW.panel_id;
 UPDATE interaction_intents SET state='cancelled',actor_id=0 WHERE chat_id=NEW.chat_id AND panel_id=NEW.panel_id AND state='pending';
 INSERT INTO interaction_cleanup(chat_id,message_id) VALUES(NEW.chat_id,NEW.panel_id) ON CONFLICT DO NOTHING;
END;
-- +goose StatementEnd
ALTER TABLE daily_menu_previews ADD COLUMN revision INTEGER NOT NULL DEFAULT 1;
-- +goose StatementBegin
CREATE TRIGGER interaction_retire_prompt AFTER UPDATE OF state ON interaction_intents
WHEN OLD.prompt_id>0 AND OLD.state='pending' AND NEW.state<>'pending'
BEGIN
 INSERT INTO interaction_cleanup(chat_id,message_id) VALUES(OLD.chat_id,OLD.prompt_id) ON CONFLICT DO NOTHING;
END;
-- +goose StatementEnd
-- +goose Down
DROP TRIGGER interaction_retire_prompt;
DROP TRIGGER interaction_close_watch;
DROP TRIGGER interaction_update_watch;
DROP TABLE interaction_cleanup;
ALTER TABLE daily_menu_previews DROP COLUMN revision;
ALTER TABLE interaction_intents RENAME TO daily_menu_intents;
ALTER TABLE interaction_panels RENAME TO daily_menu_panels;

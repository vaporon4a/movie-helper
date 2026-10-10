-- +goose Up
ALTER TABLE meme_rejections ADD COLUMN decision TEXT NOT NULL DEFAULT 'reject';
ALTER TABLE meme_rejections ADD COLUMN assessment TEXT NOT NULL DEFAULT '';
-- Only known-unsent AI memes are retired. Human proposals and uncertain sends survive.
UPDATE items SET state='rejected' WHERE kind='meme' AND ai_approved=1
 AND generation_policy!='meme-v4'
 AND NOT EXISTS (SELECT 1 FROM deliveries d WHERE d.item_id=items.id AND d.state IN ('sending','unknown','sent'))
 AND (state IN ('pending','approved') OR (state='reserved' AND id IN
 (SELECT item_id FROM deliveries WHERE state IN ('ready','retry'))));
UPDATE deliveries SET state='preparing',item_id=NULL,next_attempt=0,
 fetch_claimed=0,fetch_attempts=0,preparation_error=''
 WHERE kind='meme' AND state IN ('ready','retry') AND item_id IN
 (SELECT id FROM items WHERE ai_approved=1 AND generation_policy!='meme-v4');
UPDATE ai_work SET state='pending',next_attempt=0,claimed_until=0,attempts=0,last_error_code=''
 WHERE kind='meme_refill' AND scope_id IN
 (SELECT DISTINCT chat_id FROM items WHERE kind='meme' AND ai_approved=1 AND generation_policy!='meme-v4' AND state='rejected');
-- +goose Down
-- Old approvals are not restored automatically; recovery requires a compatible backup.
ALTER TABLE meme_rejections DROP COLUMN assessment;
ALTER TABLE meme_rejections DROP COLUMN decision;

-- +goose Up
CREATE TABLE fact_source_cache (
 cache_key TEXT PRIMARY KEY,
 payload BLOB NOT NULL,
 expires_at INTEGER NOT NULL
);
CREATE TABLE fact_source_attempts (
 chat_id INTEGER NOT NULL REFERENCES chats(chat_id),
 movie_id INTEGER NOT NULL,
 checked_at INTEGER NOT NULL,
 PRIMARY KEY(chat_id,movie_id)
);
-- Retire only known-unsent AI items. Ambiguous sends and human proposals survive.
UPDATE items SET state='rejected' WHERE kind='fact' AND ai_approved=1
 AND generation_policy!='fact-extractive-ru-v1'
 AND NOT EXISTS (SELECT 1 FROM deliveries d WHERE d.item_id=items.id AND d.state IN ('sending','unknown','sent')) AND
 (state IN ('pending','approved') OR (state='reserved' AND id IN
 (SELECT item_id FROM deliveries WHERE state IN ('ready','retry'))));
UPDATE deliveries SET state='preparing',item_id=NULL,next_attempt=0,
 fetch_claimed=0,fetch_attempts=0,preparation_error=''
 WHERE kind='fact' AND state IN ('ready','retry') AND item_id IN
 (SELECT id FROM items WHERE ai_approved=1 AND generation_policy!='fact-extractive-ru-v1');
UPDATE ai_work SET state='pending',next_attempt=0,claimed_until=0,attempts=0,last_error_code=''
 WHERE kind='fact_refill' AND scope_id IN (SELECT DISTINCT chat_id FROM items WHERE kind='fact' AND ai_approved=1 AND generation_policy!='fact-extractive-ru-v1' AND state='rejected');

-- +goose Down
-- Retirement is intentionally irreversible; restoring old text requires a backup.
DROP TABLE fact_source_attempts;
DROP TABLE fact_source_cache;

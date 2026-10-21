-- +goose Up
-- Repair only series stopped by the old waiting deadline before any confirmed
-- publication. User cancellations, disabled auto-review and finished research
-- remain terminal. Existing cancelled questions are never sent again.
UPDATE club_review_sessions
SET state='queued', reason='queue_deadline_removed'
WHERE ((state='expired' AND reason='start_deadline') OR (state='partial' AND reason='deadline'))
 AND EXISTS(SELECT 1 FROM club_watches w WHERE w.id=watch_id AND w.state='active' AND w.revision=1)
 AND COALESCE((SELECT auto_review FROM club_profile_settings s WHERE s.chat_id=club_review_sessions.chat_id),1)=1
 AND NOT EXISTS(SELECT 1 FROM club_profile_polls p WHERE p.session_id=club_review_sessions.id AND p.opened_at>0 AND p.message_id>0);

-- Zero means the discussion has not started. Unknown delivery is not a receipt.
-- Keep the obsolete start_before column for schema compatibility; application
-- code no longer reads it. The window is 28 elapsed days from first publication.
UPDATE club_review_sessions
SET start_before=0, finish_before=COALESCE((
 SELECT MIN(p.opened_at)+2419200 FROM club_profile_polls p
 WHERE p.session_id=club_review_sessions.id AND p.opened_at>0 AND p.message_id>0
),0)
WHERE state IN ('queued','researching','blocked');

UPDATE club_watch_drafts SET version=version+1
WHERE state='saved' AND watch_id IN (
 SELECT watch_id FROM club_review_sessions WHERE state IN ('queued','researching','blocked')
);

-- +goose Down
-- Data repair is intentionally not reversed: receipts or user decisions made
-- after the upgrade must not be overwritten. No schema was changed.
SELECT 1;

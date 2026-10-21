package storage

import (
	"context"
	"time"

	"github.com/vaporon4a/movie-helper/internal/clubprofile"
)

func (s *Store) DueProfileCleanup(ctx context.Context, chat int64, now time.Time) ([]clubprofile.CleanupJob, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT c.poll_id,c.chat_id,c.message_id,c.state,c.next_attempt,c.deadline,c.attempts,c.reason FROM club_poll_cleanup c JOIN club_profile_polls p ON p.id=c.poll_id WHERE c.chat_id=? AND EXISTS(SELECT 1 FROM chats WHERE chat_id=c.chat_id AND active=1) AND c.state='pending' AND c.next_attempt<=? AND p.state='completed' AND p.result_json IS NOT NULL AND p.cleanup_eligible=1 ORDER BY c.poll_id LIMIT 20`, chat, now.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []clubprofile.CleanupJob
	for rows.Next() {
		var j clubprofile.CleanupJob
		var next, deadline int64
		if err = rows.Scan(&j.PollID, &j.ChatID, &j.MessageID, &j.State, &next, &deadline, &j.Attempts, &j.Reason); err != nil {
			return nil, err
		}
		j.NextAttempt, j.Deadline = time.Unix(next, 0), time.Unix(deadline, 0)
		out = append(out, j)
	}
	return out, rows.Err()
}

func (s *Store) ClaimProfileCleanup(ctx context.Context, id int64, now time.Time) (bool, error) {
	r, err := s.db.ExecContext(ctx, "UPDATE club_poll_cleanup SET next_attempt=?,attempts=attempts+1 WHERE poll_id=? AND state='pending' AND next_attempt<=? AND deadline>?", now.Add(time.Minute).Unix(), id, now.Unix(), now.Unix())
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	return n == 1, err
}

func (s *Store) SaveProfileCleanup(ctx context.Context, id int64, state clubprofile.CleanupState, next time.Time, reason string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE club_poll_cleanup SET state=?,next_attempt=?,reason=? WHERE poll_id=? AND state='pending'", state, next.Unix(), reason, id)
	return err
}

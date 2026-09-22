package storage

import (
	"context"
	"time"

	"github.com/vaporon4a/movie-helper/internal/daily"
)

func (s *Store) Preparing(ctx context.Context) ([]daily.Delivery, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,chat_id,kind,local_date,deadline,next_attempt,fetch_attempts FROM deliveries WHERE state='preparing' AND fetch_claimed=0 ORDER BY next_attempt,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []daily.Delivery
	for rows.Next() {
		var d daily.Delivery
		if err := rows.Scan(&d.ID, &d.ChatID, &d.Kind, &d.Date, &d.Deadline, &d.NextAttempt, &d.FetchAttempts); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) ClaimPreparation(ctx context.Context, id int64, now time.Time) (bool, error) {
	r, err := s.db.ExecContext(ctx, `UPDATE deliveries SET fetch_claimed=1,fetch_attempts=fetch_attempts+1
 WHERE id=? AND state='preparing' AND fetch_claimed=0 AND fetch_attempts<? AND next_attempt<=? AND deadline>?
 AND EXISTS(SELECT 1 FROM schedules s JOIN chats c USING(chat_id) WHERE s.chat_id=deliveries.chat_id AND s.kind=deliveries.kind AND s.enabled=1 AND c.active=1 AND s.effective<deliveries.slot_at)`, id, daily.MaxPreparationAttempts, now.Unix(), now.Unix())
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	return n == 1, err
}

// A zero next attempt ends preparation. Settings changes win over stale fetches.
func (s *Store) DeferPreparation(ctx context.Context, id int64, next time.Time) error {
	state := "preparing"
	if next.IsZero() {
		state = "skipped"
	}
	_, err := s.db.ExecContext(ctx, `UPDATE deliveries SET state=?,next_attempt=?,fetch_claimed=0 WHERE id=? AND state='preparing'`, state, next.Unix(), id)
	return err
}

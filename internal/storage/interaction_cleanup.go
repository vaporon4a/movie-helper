package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/vaporon4a/movie-helper/internal/interaction"
)

func (s *Store) ExpireInteractions(ctx context.Context, now time.Time) error {
	return s.transaction(ctx, nil, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "UPDATE interaction_intents SET state='expired',actor_id=0 WHERE expires_at<=? AND state='pending'", now.Unix())
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE interaction_intents SET actor_id=0 WHERE expires_at<=?", now.Unix()); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO interaction_cleanup(chat_id,message_id) SELECT chat_id,message_id FROM interaction_surfaces WHERE state='confirmed' AND message_id>0 AND expires_at<=? ON CONFLICT DO NOTHING", now.Unix()); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM interaction_surfaces WHERE state IN ('confirmed','rejected') AND expires_at<=?", now.Unix()); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM schedule_dialogs WHERE expires_at<=?", now.Unix()); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM recovery_confirmations WHERE expires_at<=?", now.Unix()); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "DELETE FROM interaction_intents WHERE expires_at<=? AND state NOT IN ('sending','unknown','pending') AND prompt_id=0 AND NOT EXISTS(SELECT 1 FROM daily_menu_previews p WHERE p.token=interaction_intents.token AND p.state IN ('sending','unknown'))", now.Add(-24*time.Hour).Unix())
		return err
	})
}
func (s *Store) ClaimUICleanup(ctx context.Context, now time.Time) (interaction.Cleanup, bool, error) {
	var j interaction.Cleanup
	found := false
	err := s.transaction(ctx, nil, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, "SELECT chat_id,message_id,attempts FROM interaction_cleanup WHERE next_at<=? ORDER BY next_at,chat_id,message_id LIMIT 1", now.Unix()).Scan(&j.ChatID, &j.MessageID, &j.Attempts)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE interaction_cleanup SET next_at=?,attempts=attempts+1 WHERE chat_id=? AND message_id=?", now.Add(time.Minute).Unix(), j.ChatID, j.MessageID)
		found = err == nil
		return err
	})
	return j, found, err
}
func (s *Store) FinishUICleanup(ctx context.Context, j interaction.Cleanup, done bool, now time.Time) error {
	if done {
		return s.transaction(ctx, nil, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, "UPDATE interaction_intents SET prompt_id=0 WHERE chat_id=? AND prompt_id=? AND state!='pending'", j.ChatID, j.MessageID); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, "DELETE FROM interaction_cleanup WHERE chat_id=? AND message_id=?", j.ChatID, j.MessageID)
			return err
		})
	}
	delay := max(time.Minute*time.Duration(1<<min(j.Attempts, 6)), j.RetryAfter)
	_, err := s.db.ExecContext(ctx, "UPDATE interaction_cleanup SET next_at=? WHERE chat_id=? AND message_id=?", now.Add(delay).Unix(), j.ChatID, j.MessageID)
	return err
}

package storage

import (
	"context"
	"database/sql"
	"time"

	"github.com/vaporon4a/movie-helper/internal/daily"
)

func (s *Store) ClaimDailyPreview(ctx context.Context, i daily.MenuIntent, now time.Time) error {
	return s.transaction(ctx, nil, func(tx *sql.Tx) error {
		r, err := tx.ExecContext(ctx, `UPDATE daily_menu_intents SET state='sending' WHERE token=? AND chat_id=? AND panel_id=? AND action='preview' AND state='pending' AND expires_at>? AND NOT EXISTS(SELECT 1 FROM daily_menu_panels p WHERE p.chat_id=daily_menu_intents.chat_id AND p.panel_id=daily_menu_intents.panel_id AND p.closed=1)`, i.Token, i.ChatID, i.PanelID, now.Unix())
		if err != nil {
			return err
		}
		n, err := r.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return daily.ErrConflict
		}
		r, err = tx.ExecContext(ctx, `INSERT INTO daily_menu_previews(chat_id,kind,token,state,until_at) VALUES(?,?,?,'sending',?) ON CONFLICT(chat_id,kind) DO UPDATE SET token=excluded.token,state='sending',until_at=excluded.until_at,message_id=0 WHERE daily_menu_previews.state NOT IN ('sending','unknown') AND daily_menu_previews.until_at<=?`, i.ChatID, i.Kind, i.Token, now.Add(time.Minute).Unix(), now.Unix())
		if err != nil {
			return err
		}
		n, err = r.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return daily.ErrConflict
		}
		return nil
	})
}
func (s *Store) FinishDailyPreview(ctx context.Context, i daily.MenuIntent, state string, message int, now time.Time) (bool, error) {
	closed := false
	err := s.transaction(ctx, nil, func(tx *sql.Tx) error {
		var current string
		if err := tx.QueryRowContext(ctx, "SELECT state FROM daily_menu_intents WHERE token=?", i.Token).Scan(&current); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM daily_menu_panels WHERE chat_id=? AND panel_id=? AND closed=1)", i.ChatID, i.PanelID).Scan(&closed); err != nil {
			return err
		}
		closed = closed || current == cancelledState
		if _, err := tx.ExecContext(ctx, "UPDATE daily_menu_previews SET state=?,until_at=?,message_id=? WHERE chat_id=? AND kind=? AND token=?", state, now.Add(30*time.Second).Unix(), message, i.ChatID, i.Kind, i.Token); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "UPDATE daily_menu_intents SET state=? WHERE token=?", state, i.Token)
		return err
	})
	return closed, err
}

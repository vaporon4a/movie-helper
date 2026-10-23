package storage

import (
	"context"
	"database/sql"
	"errors"

	"github.com/vaporon4a/movie-helper/internal/daily"
)

func (s *Store) PanelScreen(ctx context.Context, chat int64, panel int) (string, error) {
	var screen string
	err := s.db.QueryRowContext(ctx, "SELECT screen FROM interaction_panels WHERE chat_id=? AND panel_id=?", chat, panel).Scan(&screen)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return screen, err
}
func (s *Store) SetPanelScreen(ctx context.Context, chat int64, panel int, screen string) error {
	return s.transaction(ctx, nil, func(tx *sql.Tx) error {
		var closed bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM interaction_panels WHERE chat_id=? AND panel_id=? AND closed=1)", chat, panel).Scan(&closed); err != nil {
			return err
		}
		if closed {
			return daily.ErrConflict
		}
		if _, err := tx.ExecContext(ctx, "UPDATE interaction_intents SET state='cancelled',actor_id=0 WHERE chat_id=? AND panel_id=? AND state='pending'", chat, panel); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM schedule_dialogs WHERE chat_id=? AND panel_id=?", chat, panel); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM recovery_confirmations WHERE chat_id=? AND panel_id=?", chat, panel); err != nil {
			return err
		}
		// Navigation abandons only an unconfirmed viewing, never an existing record.
		if _, err := tx.ExecContext(ctx, "UPDATE club_watch_drafts SET state='expired',reason='dialog_left',rendered=version+1,version=version+1 WHERE chat_id=? AND message_id=? AND json_extract(input_json,'$.provisional')=1 AND state IN ('choice','pending')", chat, panel); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO interaction_panels(chat_id,panel_id,screen) VALUES(?,?,?) ON CONFLICT(chat_id,panel_id) DO UPDATE SET screen=excluded.screen", chat, panel, screen)
		return err
	})
}

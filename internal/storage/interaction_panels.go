package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/vaporon4a/movie-helper/internal/interaction"
)

func (s *Store) AcquirePanel(ctx context.Context, chat, requester int64, purpose string, now time.Time) (interaction.Panel, error) {
	p := interaction.Panel{ChatID: chat, Requester: requester, Purpose: purpose}
	err := s.transaction(ctx, nil, func(tx *sql.Tx) error {
		var expires int64
		var closed bool
		err := tx.QueryRowContext(ctx, "SELECT token,state,message_id,expires_at,EXISTS(SELECT 1 FROM interaction_panels p WHERE p.chat_id=s.chat_id AND p.panel_id=s.message_id AND p.closed=1) FROM interaction_surfaces s WHERE chat_id=? AND requester=? AND purpose=?", chat, requester, purpose).Scan(&p.Token, &p.State, &p.MessageID, &expires, &closed)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil && !closed && reusableSurface(p, expires, now) {
			return refreshSurface(ctx, tx, p, now)
		}
		if p.MessageID > 0 {
			if _, err = tx.ExecContext(ctx, "INSERT INTO interaction_cleanup(chat_id,message_id) VALUES(?,?) ON CONFLICT DO NOTHING", chat, p.MessageID); err != nil {
				return err
			}
		}
		p.Token, err = interaction.Token()
		if err != nil {
			return err
		}
		p.MessageID = -1
		p.State = "claimed"
		_, err = tx.ExecContext(ctx, "INSERT INTO interaction_surfaces(chat_id,requester,purpose,token,state,expires_at) VALUES(?,?,?,?,'sending',?) ON CONFLICT(chat_id,requester,purpose) DO UPDATE SET token=excluded.token,state='sending',message_id=-1,expires_at=excluded.expires_at", chat, requester, purpose, p.Token, now.Add(24*time.Hour).Unix())
		return err
	})
	return p, err
}
func (s *Store) FinishPanel(ctx context.Context, p interaction.Panel, message int, state string, now time.Time) error {
	r, err := s.db.ExecContext(ctx, "UPDATE interaction_surfaces SET message_id=?,state=?,expires_at=? WHERE chat_id=? AND requester=? AND purpose=? AND token=? AND state='sending'", message, state, now.Add(24*time.Hour).Unix(), p.ChatID, p.Requester, p.Purpose, p.Token)
	return profileChanged(r, err)
}
func (s *Store) RetirePanel(ctx context.Context, p interaction.Panel) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM interaction_surfaces WHERE chat_id=? AND requester=? AND purpose=? AND token=? AND state='confirmed'", p.ChatID, p.Requester, p.Purpose, p.Token)
	return err
}

func reusableSurface(p interaction.Panel, expires int64, now time.Time) bool {
	return p.State == "sending" || p.State == unknownState || (p.State == "confirmed" && expires > now.Unix())
}

func refreshSurface(ctx context.Context, tx *sql.Tx, p interaction.Panel, now time.Time) error {
	if p.State != "confirmed" {
		return nil
	}
	_, err := tx.ExecContext(ctx, "UPDATE interaction_surfaces SET expires_at=? WHERE chat_id=? AND requester=? AND purpose=?", now.Add(24*time.Hour).Unix(), p.ChatID, p.Requester, p.Purpose)
	return err
}

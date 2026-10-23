package storage

import (
	"context"
	"database/sql"
	"time"

	"github.com/vaporon4a/movie-helper/internal/clubprofile"
)

func (s *Store) RecoverProfilePolls(ctx context.Context, now time.Time) error {
	return s.transaction(ctx, nil, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "UPDATE club_profile_polls SET state='unknown',delivery_counted=1,error_code='restart_during_send' WHERE state='creating'"); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "UPDATE club_profile_polls SET state='open',next_attempt=?,error_code='restart_during_close' WHERE state='closing'", now.Unix())
		return err
	})
}

func (s *Store) ResolveProfilePoll(ctx context.Context, operation, chat, id int64, action string, now time.Time) error {
	return s.transaction(ctx, &operation, func(tx *sql.Tx) error {
		return resolveProfilePoll(ctx, tx, chat, id, action, now)
	})
}

func profileResolveTarget(p clubprofile.Poll, action string, now time.Time) (clubprofile.PollState, error) {
	if p.State != clubprofile.Failed && p.State != clubprofile.Unknown && p.State != clubprofile.Planned {
		return "", clubprofile.ErrConflict
	}
	if action == "cancel" {
		return clubprofile.Cancelled, nil
	}
	if action != clubprofile.ResolveRetry {
		return "", clubprofile.ErrInvalid
	}
	if p.State != clubprofile.Failed {
		return "", clubprofile.ErrConflict
	}
	if p.TelegramID != "" {
		return clubprofile.Open, nil
	}
	if now.After(p.SlotAt.Add(clubprofile.CatchupWindow)) {
		return "", clubprofile.ErrConflict
	}
	return clubprofile.Planned, nil
}

func resolveProfilePoll(ctx context.Context, tx *sql.Tx, chat, id int64, action string, now time.Time) error {

	p, err := scanProfilePoll(tx.QueryRowContext(ctx, "SELECT "+profilePollColumns+" FROM club_profile_polls WHERE id=? AND chat_id=?", id, chat))
	if err != nil {
		return err
	}
	to, err := profileResolveTarget(p, action, now)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "UPDATE club_profile_polls SET state=?,next_attempt=?,error_code='' WHERE id=?", to, now.Unix(), id)
	if err == nil && action == "cancel" && p.SessionID != 0 {
		_, err = tx.ExecContext(ctx, "UPDATE club_review_sessions SET state='partial',reason='question_cancelled' WHERE id=? AND state IN ('researching','blocked','queued')", p.SessionID)
		if err == nil {
			_, err = tx.ExecContext(ctx, "UPDATE club_profile_polls SET state='cancelled',error_code='batch_cancelled' WHERE session_id=? AND state='planned'", p.SessionID)
		}
	}
	return err
}

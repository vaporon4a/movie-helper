package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/vaporon4a/movie-helper/internal/clubprofile"
)

func (s *Store) AttachWatchDialog(ctx context.Context, chat, id int64, panel int, token string, now time.Time) error {
	return s.transaction(ctx, nil, func(tx *sql.Tx) error {
		r, err := tx.ExecContext(ctx, "UPDATE interaction_intents SET state='done',actor_id=0 WHERE token=? AND chat_id=? AND panel_id=? AND kind='watch' AND action='time' AND revision=0 AND state='pending' AND expires_at>?", token, chat, panel, now.Unix())
		if err = profileChanged(r, err); err != nil {
			return err
		}

		r, err = tx.ExecContext(ctx, "UPDATE club_watch_drafts SET message_id=? WHERE id=? AND chat_id=? AND message_id=0 AND json_extract(input_json,'$.provisional')=1 AND NOT EXISTS(SELECT 1 FROM interaction_panels WHERE chat_id=? AND panel_id=? AND closed=1)", panel, id, chat, chat, panel)
		if err = profileChanged(r, err); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO interaction_panels(chat_id,panel_id,screen) VALUES(?,?,?) ON CONFLICT(chat_id,panel_id) DO UPDATE SET screen=excluded.screen", chat, panel, fmt.Sprintf("watch:%d", id))
		return err
	})
}

func (s *Store) ConfirmWatchDialog(ctx context.Context, chat, id int64, version int, date string, now time.Time) (clubprofile.WatchDraft, error) {
	var d clubprofile.WatchDraft
	err := s.transaction(ctx, nil, func(tx *sql.Tx) error {
		var err error
		d, err = scanWatchDraft(tx.QueryRowContext(ctx, "SELECT "+watchDraftColumns+" FROM club_watch_drafts WHERE id=? AND chat_id=?", id, chat))
		if err != nil {
			return err
		}
		in, f, err := confirmedWatchInput(ctx, tx, d, version, date, now)
		if err != nil {
			return err
		}
		d.Input = in
		d.WatchID, err = saveProfileWatch(ctx, tx, d, f, now)
		if err != nil {
			return err
		}
		d.State, d.Reason = "saved", ""
		d.Version++
		raw, err := json.Marshal(d.Input)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE club_watch_drafts SET input_json=?,watch_id=?,state='saved',reason='',version=? WHERE id=?", raw, d.WatchID, d.Version, id)
		return err
	})
	return d, err
}

func (s *Store) DateWatchDialog(ctx context.Context, chat, id int64, version int, date string, now time.Time) (clubprofile.WatchDraft, error) {
	var d clubprofile.WatchDraft
	err := s.transaction(ctx, nil, func(tx *sql.Tx) error {
		var err error
		d, err = scanWatchDraft(tx.QueryRowContext(ctx, "SELECT "+watchDraftColumns+" FROM club_watch_drafts WHERE id=? AND chat_id=?", id, chat))
		if err != nil {
			return err
		}
		if d.State != choiceState || !d.Input.Provisional || d.Version != version || d.Closed || !now.Before(d.Expires) || d.Input.TMDBID == 0 {
			return clubprofile.ErrConflict
		}
		in, err := clubprofile.ParseWatchInput(fmt.Sprintf("https://www.themoviedb.org/movie/%d | %s", d.Input.TMDBID, date), d.Input.Zone, now)
		if err != nil {
			return err
		}
		in.Provisional = true
		d.Input = in
		d.Version++
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE club_watch_drafts SET input_json=?,version=? WHERE id=?", raw, d.Version, id)
		return err
	})
	return d, err
}

func confirmedWatchInput(ctx context.Context, tx *sql.Tx, d clubprofile.WatchDraft, version int, date string, now time.Time) (clubprofile.WatchInput, clubprofile.Film, error) {
	if d.State != choiceState || d.Reason != "confirm_required" || !d.Input.Provisional || d.Version != version || d.Closed || !now.Before(d.Expires) || len(d.Candidates) != 1 {
		return clubprofile.WatchInput{}, clubprofile.Film{}, clubprofile.ErrConflict
	}
	f := d.Candidates[0]
	if f.TMDBID != d.Input.TMDBID || f.Source != "tmdb" {
		return clubprofile.WatchInput{}, clubprofile.Film{}, clubprofile.ErrInvalid
	}
	var active bool
	var zone string
	if err := tx.QueryRowContext(ctx, "SELECT active,zone FROM chats WHERE chat_id=?", d.ChatID).Scan(&active, &zone); err != nil {
		return clubprofile.WatchInput{}, clubprofile.Film{}, err
	}
	if !active || zone != d.Input.Zone {
		return clubprofile.WatchInput{}, clubprofile.Film{}, clubprofile.ErrConflict
	}
	if date == "" {
		date = d.Input.Date
		if date == "" {
			date = unknownState
		}
	}
	in, err := clubprofile.ParseWatchInput(fmt.Sprintf("https://www.themoviedb.org/movie/%d | %s", f.TMDBID, date), zone, now)
	if err != nil {
		return clubprofile.WatchInput{}, clubprofile.Film{}, err
	}
	return in, f, nil
}

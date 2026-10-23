package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/vaporon4a/movie-helper/internal/clubprofile"
	"github.com/vaporon4a/movie-helper/internal/clubreminder"
	"github.com/vaporon4a/movie-helper/internal/featurevote"
	"github.com/vaporon4a/movie-helper/internal/scheduleui"
)

func scheduleDialog(ctx context.Context, tx *sql.Tx, token string, chat int64, panel int, now time.Time) (scheduleui.Draft, error) {
	var d scheduleui.Draft
	var raw string
	err := tx.QueryRowContext(ctx, "SELECT body FROM schedule_dialogs WHERE token=? AND chat_id=? AND panel_id=? AND expires_at>? AND NOT EXISTS(SELECT 1 FROM interaction_panels WHERE chat_id=? AND panel_id=? AND closed=1)", token, chat, panel, now.Unix(), chat, panel).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return d, scheduleui.ErrConflict
	}
	if err != nil {
		return d, err
	}
	err = json.Unmarshal([]byte(raw), &d)
	return d, err
}
func (s *Store) ScheduleDialog(ctx context.Context, token string, chat int64, panel int, now time.Time) (scheduleui.Draft, error) {
	var d scheduleui.Draft
	err := s.transaction(ctx, nil, func(tx *sql.Tx) error { var e error; d, e = scheduleDialog(ctx, tx, token, chat, panel, now); return e })
	return d, err
}
func (s *Store) PutScheduleDialog(ctx context.Context, d scheduleui.Draft, previous string, now time.Time) error {
	raw, err := json.Marshal(d)
	if err != nil {
		return err
	}
	return s.transaction(ctx, nil, func(tx *sql.Tx) error {
		if previous != "" {
			if _, err := scheduleDialog(ctx, tx, previous, d.ChatID, d.PanelID, now); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO schedule_dialogs(token,chat_id,panel_id,body,expires_at) VALUES(?,?,?,?,?) ON CONFLICT(chat_id,panel_id) DO UPDATE SET token=excluded.token,body=excluded.body,expires_at=excluded.expires_at", d.Token, d.ChatID, d.PanelID, raw, d.Expires.Unix())
		return err
	})
}
func (s *Store) CommitScheduleDialog(ctx context.Context, d scheduleui.Draft, now time.Time) (bool, error) {
	changed := false
	err := s.transaction(ctx, nil, func(tx *sql.Tx) error {
		current, err := scheduleDialog(ctx, tx, d.Token, d.ChatID, d.PanelID, now)
		if err != nil {
			return err
		}
		if current.Step != "confirm" {
			return scheduleui.ErrConflict
		}
		changed, err = commitScheduleConfig(ctx, tx, current, now)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "DELETE FROM schedule_dialogs WHERE token=?", current.Token)
		return err
	})
	return changed, err
}
func commitScheduleConfig(ctx context.Context, tx *sql.Tx, d scheduleui.Draft, now time.Time) (bool, error) {
	switch {
	case d.Movies != nil:
		return commitMovieDialog(ctx, tx, d, now)
	case d.Club != nil:
		return commitClubDialog(ctx, tx, d, now)
	case d.Ideas != nil:
		return commitIdeaDialog(ctx, tx, d, now)
	default:
		return false, scheduleui.ErrConflict
	}
}
func commitMovieDialog(ctx context.Context, tx *sql.Tx, d scheduleui.Draft, now time.Time) (bool, error) {
	m := d.Movies
	rev, active, zone, err := configHeader(ctx, tx, d.ChatID, "movies")
	if err != nil {
		return false, err
	}
	if rev != m.Before.Revision {
		return false, clubprofile.ErrConfigConflict
	}
	change := clubprofile.ConfigChange{Topic: m.Topic}
	for day, v := range m.Days {
		change.Days = append(change.Days, clubprofile.SchedulePatch{Weekday: day, Clock: new(v.Clock), Enabled: new(v.Enabled)})
	}
	return patchMovieDays(ctx, tx, d.ChatID, change, active && zone != "", now)
}
func commitClubDialog(ctx context.Context, tx *sql.Tx, d scheduleui.Draft, now time.Time) (bool, error) {
	c, err := readClubConfig(ctx, tx, d.ChatID)
	if err != nil {
		return false, err
	}
	if c.Revision != d.Club.Before.Revision {
		return false, clubreminder.ErrConfigConflict
	}
	v := d.Club.Value
	return patchClubSettings(ctx, tx, c, clubreminder.ConfigChange{Weekday: new(v.Weekday), Ordinal: new(v.Ordinal), Clock: new(v.Clock), Enabled: new(v.Enabled)}, now)
}
func commitIdeaDialog(ctx context.Context, tx *sql.Tx, d scheduleui.Draft, now time.Time) (bool, error) {
	c, err := readIdeasConfig(ctx, tx, d.ChatID)
	if err != nil {
		return false, err
	}
	if c.Revision != d.Ideas.Before.Revision {
		return false, featurevote.ErrConfigConflict
	}
	v := d.Ideas.Value
	return patchIdeasSettings(ctx, tx, c, featurevote.ConfigChange{Weekday: new(v.Weekday), Clock: new(v.Clock), Enabled: new(v.Enabled)}, now)
}

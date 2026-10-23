package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/vaporon4a/movie-helper/internal/clubreminder"
)

func readClubConfig(ctx context.Context, tx *sql.Tx, chat int64) (clubreminder.ScheduleConfig, error) {
	var c clubreminder.ScheduleConfig
	var err error
	c.Revision, c.Active, c.Zone, err = configHeader(ctx, tx, chat, "club")
	if err != nil {
		return c, err
	}
	c.Settings = clubreminder.Settings{ChatID: chat, Weekday: 5, Ordinal: 2, Clock: "19:00", Zone: c.Zone}
	err = tx.QueryRowContext(ctx, "SELECT weekday,ordinal,clock,enabled,revision,effective_at FROM club_reminder_settings WHERE chat_id=?", chat).Scan(&c.Settings.Weekday, &c.Settings.Ordinal, &c.Settings.Clock, &c.Settings.Enabled, &c.Settings.Revision, &c.Settings.Effective)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return c, err
}
func (s *Store) ReadClubConfig(ctx context.Context, chat int64) (clubreminder.ScheduleConfig, error) {
	var c clubreminder.ScheduleConfig
	err := s.transaction(ctx, nil, func(tx *sql.Tx) error { var e error; c, e = readClubConfig(ctx, tx, chat); return e })
	return c, err
}
func (s *Store) PatchClubConfig(ctx context.Context, chat, revision int64, change clubreminder.ConfigChange, now time.Time) (bool, error) {
	changed := false
	err := s.transaction(ctx, nil, func(tx *sql.Tx) error {
		c, err := readClubConfig(ctx, tx, chat)
		if err != nil {
			return err
		}
		if c.Revision != revision {
			return clubreminder.ErrConfigConflict
		}
		changed, err = patchClubSettings(ctx, tx, c, change, now)
		return err
	})
	return changed, err
}
func patchClubSettings(ctx context.Context, tx *sql.Tx, c clubreminder.ScheduleConfig, p clubreminder.ConfigChange, now time.Time) (bool, error) {
	next := c.Settings
	if p.Weekday != nil {
		next.Weekday = *p.Weekday
	}
	if p.Ordinal != nil {
		next.Ordinal = *p.Ordinal
	}
	if p.Clock != nil {
		next.Clock = *p.Clock
	}
	if p.Enabled != nil {
		next.Enabled = *p.Enabled
	}
	if clubreminder.Validate(next.Weekday, next.Ordinal, next.Clock) != nil || (next.Enabled && !c.Settings.Enabled && (!c.Active || c.Zone == "")) {
		return false, clubreminder.ErrConfigConflict
	}
	if next.Weekday == c.Settings.Weekday && next.Ordinal == c.Settings.Ordinal && next.Clock == c.Settings.Clock && next.Enabled == c.Settings.Enabled {
		return false, nil
	}
	if err := cancelClub(ctx, tx, c.Settings.ChatID, now); err != nil {
		return false, err
	}
	_, err := tx.ExecContext(ctx, "INSERT INTO club_reminder_settings(chat_id,weekday,ordinal,clock,enabled,revision,effective_at) VALUES(?,?,?,?,?,1,?) ON CONFLICT(chat_id) DO UPDATE SET weekday=excluded.weekday,ordinal=excluded.ordinal,clock=excluded.clock,enabled=excluded.enabled,revision=revision+1,effective_at=excluded.effective_at", c.Settings.ChatID, next.Weekday, next.Ordinal, next.Clock, next.Enabled, now.Unix())
	return true, err
}

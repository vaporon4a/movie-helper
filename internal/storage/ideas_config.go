package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/vaporon4a/movie-helper/internal/featurevote"
)

func readIdeasConfig(ctx context.Context, tx *sql.Tx, chat int64) (featurevote.ScheduleConfig, error) {
	var c featurevote.ScheduleConfig
	var err error
	c.Revision, c.Active, c.Zone, err = configHeader(ctx, tx, chat, "ideas")
	if err != nil {
		return c, err
	}
	c.Schedule = featurevote.Schedule{ChatID: chat, Weekday: 0, Clock: "12:00", Zone: c.Zone}
	err = tx.QueryRowContext(ctx, "SELECT weekday,clock,enabled,effective FROM feature_vote_schedules WHERE chat_id=?", chat).Scan(&c.Schedule.Weekday, &c.Schedule.Clock, &c.Schedule.Enabled, &c.Schedule.Effective)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return c, err
}
func (s *Store) ReadIdeasConfig(ctx context.Context, chat int64) (featurevote.ScheduleConfig, error) {
	var c featurevote.ScheduleConfig
	err := s.transaction(ctx, nil, func(tx *sql.Tx) error { var e error; c, e = readIdeasConfig(ctx, tx, chat); return e })
	return c, err
}
func (s *Store) PatchIdeasConfig(ctx context.Context, chat, revision int64, change featurevote.ConfigChange, now time.Time) (bool, error) {
	changed := false
	err := s.transaction(ctx, nil, func(tx *sql.Tx) error {
		c, err := readIdeasConfig(ctx, tx, chat)
		if err != nil {
			return err
		}
		if c.Revision != revision {
			return featurevote.ErrConfigConflict
		}
		changed, err = patchIdeasSettings(ctx, tx, c, change, now)
		return err
	})
	return changed, err
}
func patchIdeasSettings(ctx context.Context, tx *sql.Tx, c featurevote.ScheduleConfig, p featurevote.ConfigChange, now time.Time) (bool, error) {
	next := c.Schedule
	if p.Weekday != nil {
		next.Weekday = *p.Weekday
	}
	if p.Clock != nil {
		next.Clock = *p.Clock
	}
	if p.Enabled != nil {
		next.Enabled = *p.Enabled
	}
	if next.Weekday < 0 || next.Weekday > 6 || !validConfigClock(next.Clock) || (next.Enabled && !c.Schedule.Enabled && (!c.Active || c.Zone == "")) {
		return false, featurevote.ErrConfigConflict
	}
	if next.Weekday == c.Schedule.Weekday && next.Clock == c.Schedule.Clock && next.Enabled == c.Schedule.Enabled {
		return false, nil
	}
	if _, err := tx.ExecContext(ctx, "UPDATE feature_rounds SET state='cancelled',error_code='schedule_changed' WHERE chat_id=? AND state='planned' AND parent_id IS NULL", next.ChatID); err != nil {
		return false, err
	}
	_, err := tx.ExecContext(ctx, "INSERT INTO feature_vote_schedules(chat_id,weekday,clock,enabled,effective) VALUES(?,?,?,?,?) ON CONFLICT(chat_id) DO UPDATE SET weekday=excluded.weekday,clock=excluded.clock,enabled=excluded.enabled,effective=excluded.effective", next.ChatID, next.Weekday, next.Clock, next.Enabled, now.Unix())
	return true, err
}

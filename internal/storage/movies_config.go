package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/vaporon4a/movie-helper/internal/clubprofile"
)

func (s *Store) ReadMoviesConfig(ctx context.Context, chat int64) (clubprofile.ScheduleConfig, error) {
	var c clubprofile.ScheduleConfig
	err := s.transaction(ctx, nil, func(tx *sql.Tx) error {
		var err error
		c.Revision, c.Active, c.Zone, err = configHeader(ctx, tx, chat, "movies")
		if err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, "SELECT id,topic,weekday,clock,enabled,effective FROM club_profile_schedules WHERE chat_id=? ORDER BY topic,weekday", chat)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			v := clubprofile.Schedule{ChatID: chat, Zone: c.Zone}
			var effective int64
			if err = rows.Scan(&v.ID, &v.Topic, &v.Weekday, &v.Clock, &v.Enabled, &effective); err != nil {
				return err
			}
			v.Effective = time.Unix(effective, 0)
			c.Schedules = append(c.Schedules, v)
		}
		return rows.Err()
	})
	return c, err
}
func (s *Store) PatchMoviesConfig(ctx context.Context, chat, revision int64, change clubprofile.ConfigChange, now time.Time) (bool, error) {
	changed := false
	err := s.transaction(ctx, nil, func(tx *sql.Tx) error {
		rev, active, zone, err := configHeader(ctx, tx, chat, "movies")
		if err != nil {
			return err
		}
		if rev != revision {
			return clubprofile.ErrConfigConflict
		}
		changed, err = patchMovieDays(ctx, tx, chat, change, active && zone != "", now)
		return err
	})
	return changed, err
}
func patchMovieDays(ctx context.Context, tx *sql.Tx, chat int64, change clubprofile.ConfigChange, canEnable bool, now time.Time) (bool, error) {
	if change.Topic != clubprofile.Genre && change.Topic != clubprofile.Reference {
		return false, clubprofile.ErrConfigConflict
	}
	changed := false
	seen := map[int]bool{}
	for _, patch := range change.Days {
		if patch.Weekday < 0 || patch.Weekday > 6 || seen[patch.Weekday] {
			return false, clubprofile.ErrConfigConflict
		}
		seen[patch.Weekday] = true
		dayChanged, err := patchMovieDay(ctx, tx, chat, change.Topic, patch, canEnable, now)
		if err != nil {
			return false, err
		}
		changed = changed || dayChanged
	}
	if changed {
		return true, cancelProfilePlanned(ctx, tx, chat, change.Topic, "schedule_changed")
	}
	return false, nil
}

func patchMovieDay(ctx context.Context, tx *sql.Tx, chat int64, topic clubprofile.Topic, patch clubprofile.SchedulePatch, canEnable bool, now time.Time) (bool, error) {
	clock := "09:00"
	enabled := false
	err := tx.QueryRowContext(ctx, "SELECT clock,enabled FROM club_profile_schedules WHERE chat_id=? AND topic=? AND weekday=?", chat, topic, patch.Weekday).Scan(&clock, &enabled)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	nextClock, nextEnabled := clock, enabled
	if patch.Clock != nil {
		nextClock = *patch.Clock
	}
	if patch.Enabled != nil {
		nextEnabled = *patch.Enabled
	}
	if !validConfigClock(nextClock) || (nextEnabled && !enabled && !canEnable) {
		return false, clubprofile.ErrConfigConflict
	}
	if clock == nextClock && enabled == nextEnabled {
		return false, nil
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO club_profile_schedules(chat_id,topic,weekday,clock,enabled,effective) VALUES(?,?,?,?,?,?) ON CONFLICT(chat_id,topic,weekday) DO UPDATE SET clock=excluded.clock,enabled=excluded.enabled,effective=excluded.effective", chat, topic, patch.Weekday, nextClock, nextEnabled, now.Unix())
	if err != nil {
		return false, err
	}
	return true, nil
}

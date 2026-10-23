package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/vaporon4a/movie-helper/internal/clubprofile"
)

func (s *Store) ProfileSchedules(ctx context.Context, chat int64) ([]clubprofile.Schedule, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT p.id,p.chat_id,p.topic,p.weekday,p.clock,p.enabled AND c.active,p.effective,c.zone,
 COALESCE((SELECT json_group_object(week,role) FROM club_slot_roles WHERE schedule_id=p.id),'{}')
 FROM club_profile_schedules p JOIN chats c USING(chat_id) WHERE (?=0 OR p.chat_id=?) ORDER BY p.chat_id,p.weekday,p.clock,p.id`, chat, chat)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []clubprofile.Schedule
	for rows.Next() {
		var p clubprofile.Schedule
		var effective int64
		var roles string
		if err = rows.Scan(&p.ID, &p.ChatID, &p.Topic, &p.Weekday, &p.Clock, &p.Enabled, &effective, &p.Zone, &roles); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(roles), &p.WeekRoles); err != nil {
			return nil, err
		}
		p.Effective = time.Unix(effective, 0)
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) SetProfileSchedule(ctx context.Context, operation int64, p clubprofile.Schedule) error {
	if (p.Topic != clubprofile.Genre && p.Topic != clubprofile.Reference) || p.Weekday < 0 || p.Weekday > 6 || len(p.Clock) != 5 {
		return clubprofile.ErrInvalid
	}
	if _, err := time.Parse("15:04", p.Clock); err != nil {
		return err
	}
	return s.transaction(ctx, &operation, func(tx *sql.Tx) error {
		_, active, zone, err := configHeader(ctx, tx, p.ChatID, "movies")
		if err != nil {
			return err
		}
		patch := clubprofile.SchedulePatch{Weekday: p.Weekday, Enabled: new(p.Enabled)}
		if p.Enabled {
			patch.Clock = new(p.Clock)
		}
		_, err = patchMovieDays(ctx, tx, p.ChatID, clubprofile.ConfigChange{Topic: p.Topic, Days: []clubprofile.SchedulePatch{patch}}, active && zone != "", p.Effective)
		return err
	})
}

func (s *Store) PauseProfileSchedules(ctx context.Context, operation, chat int64, topic clubprofile.Topic, now time.Time) error {
	if topic != clubprofile.Genre && topic != clubprofile.Reference {
		return clubprofile.ErrInvalid
	}
	return s.transaction(ctx, &operation, func(tx *sql.Tx) error {
		r, err := tx.ExecContext(ctx, "UPDATE club_profile_schedules SET enabled=0,effective=? WHERE chat_id=? AND topic=? AND enabled=1", now.Unix(), chat, topic)
		if err != nil {
			return err
		}
		n, err := r.RowsAffected()
		if err != nil || n == 0 {
			return err
		}
		return cancelProfilePlanned(ctx, tx, chat, topic, "schedule_paused")
	})
}

func cancelProfilePlanned(ctx context.Context, tx *sql.Tx, chat int64, topic clubprofile.Topic, reason string) error {
	_, err := tx.ExecContext(ctx, `UPDATE club_profile_polls SET state='cancelled',error_code=? WHERE chat_id=? AND (?='' OR topic=? OR batch_id IN (
 SELECT b.id FROM club_poll_batches b JOIN club_profile_schedules s ON b.occurrence LIKE 'schedule:'||s.id||':%' WHERE s.chat_id=? AND s.topic=?)) AND state='planned'`, reason, chat, topic, topic, chat, topic)
	return err
}

func updateProfileZone(ctx context.Context, tx *sql.Tx, chat int64, now time.Time) error {
	exists, err := tableExists(ctx, tx, "club_profile_schedules")
	if err != nil || !exists {
		return err
	}
	if err = cancelProfilePlanned(ctx, tx, chat, "", "timezone_changed"); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "UPDATE club_profile_schedules SET effective=? WHERE chat_id=?", now.Unix(), chat)
	return err
}

func suspendProfile(ctx context.Context, tx *sql.Tx, chat int64) error {
	exists, err := tableExists(ctx, tx, "club_profile_schedules")
	if err != nil || !exists {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE club_profile_schedules SET enabled=0 WHERE chat_id=?", chat); err != nil {
		return err
	}
	return cancelProfilePlanned(ctx, tx, chat, "", "chat_suspended")
}

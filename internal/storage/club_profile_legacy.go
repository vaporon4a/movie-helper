package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/vaporon4a/movie-helper/internal/clubprofile"
)

// MigrateClubProfile is atomic: an unreadable old snapshot prevents cutover.
func (s *Store) MigrateClubProfile(ctx context.Context, now time.Time) error {
	return s.transaction(ctx, nil, func(tx *sql.Tx) error {
		var done int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM club_profile_migration WHERE version=1").Scan(&done); err != nil {
			return err
		}
		if done != 0 {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO club_profile_schedules(id,chat_id,topic,weekday,clock,enabled,effective)
 SELECT id,chat_id,feature,weekday,clock,enabled,effective FROM movie_poll_schedules`); err != nil {
			return err
		}
		if err := importProfileRounds(ctx, tx, now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE movie_rounds SET state='cancelled',error_code='profile_collected'
 WHERE state IN ('selecting','ready')`); err != nil {
			return err
		}
		// Неопределённую отправку не объявляем успешной и не повторяем.
		if _, err := tx.ExecContext(ctx, `UPDATE movie_rounds SET state='unknown',error_code='restart_during_network' WHERE state='publishing'`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE movie_rounds SET page2_state=CASE WHEN page2_state='sending' THEN 'unknown' ELSE 'none' END WHERE page2_state IN ('ready','sending')`); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO club_profile_migration(version,applied_at) VALUES(1,?)", now.Unix())
		return err
	})
}

func importProfileRounds(ctx context.Context, tx *sql.Tx, now time.Time) error {
	old, err := loadLegacyRounds(ctx, tx)
	if err != nil {
		return err
	}
	for _, r := range old {
		q, counts, e := loadLegacyQuestion(ctx, tx, r)
		if e != nil {
			return e
		}
		question, e := json.Marshal(q)
		if e != nil {
			return e
		}
		state, closed := legacyProfileState(r)
		result, e := json.Marshal(clubprofile.PollResult{PollID: r.PollID, Counts: counts, Closed: closed, Source: clubprofile.SourceLegacy, ReceivedAt: now})
		if e != nil {
			return e
		}
		duration := r.ClosesAt - r.SlotAt
		if duration <= 0 {
			duration = int64(clubprofile.DefaultDuration / time.Second)
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO club_profile_polls(id,chat_id,topic,legacy_id,occurrence,state,question_json,telegram_poll_id,message_id,slot_at,opened_at,closes_at,duration,next_attempt,error_code,result_json,cleanup_eligible)
 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,0)`, r.ID, r.ChatID, r.Feature, r.ID, fmt.Sprintf("slot:%s:%d", r.Feature, r.SlotAt), state, question, r.PollID, r.PollMessageID, r.SlotAt, r.OpenedAt, r.ClosesAt, duration, r.NextAttempt, r.ErrorCode, result)
		if e != nil {
			return e
		}
	}
	return nil
}

func loadLegacyRounds(ctx context.Context, tx *sql.Tx) ([]legacyProfileRound, error) {
	rows, err := tx.QueryContext(ctx, "SELECT id,chat_id,feature,state,telegram_poll_id,poll_message_id,slot_at,opened_at,closes_at,next_attempt,error_code FROM movie_rounds ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []legacyProfileRound
	for rows.Next() {
		var r legacyProfileRound
		e := rows.Scan(&r.ID, &r.ChatID, &r.Feature, &r.State, &r.PollID, &r.PollMessageID, &r.SlotAt, &r.OpenedAt, &r.ClosesAt, &r.NextAttempt, &r.ErrorCode)
		if e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func loadLegacyQuestion(ctx context.Context, tx *sql.Tx, r legacyProfileRound) (clubprofile.Question, []int, error) {
	q := clubprofile.Question{Topic: clubprofile.Topic(r.Feature), Intent: clubprofile.LegacyInterest, Scale: "legacy-unknown", Version: "legacy", Source: "legacy", Anonymous: true}
	rows, err := tx.QueryContext(ctx, "SELECT position,option_kind,provider_id,label,votes,metadata_json FROM movie_poll_options WHERE round_id=? ORDER BY position", r.ID)
	if err != nil {
		return q, nil, err
	}
	defer rows.Close()
	var counts []int
	for rows.Next() {
		var position, count int
		var kind string
		var o clubprofile.Option
		if err = rows.Scan(&position, &kind, &o.ProviderID, &o.Text, &count, &o.Metadata); err != nil {
			return q, nil, err
		}
		if position != len(q.Options) || count < 0 || !json.Valid([]byte(o.Metadata)) {
			return q, nil, clubprofile.ErrInvalid
		}
		o.Key = fmt.Sprintf("%s:%d", kind, o.ProviderID)
		o.Substantive = true
		q.Options = append(q.Options, o)
		counts = append(counts, count)
	}
	return q, counts, rows.Err()
}

func legacyProfileState(r legacyProfileRound) (clubprofile.PollState, bool) {
	switch r.State {
	case "open", "closing":
		return clubprofile.Open, false
	case "selecting", "ready", "published":
		return clubprofile.Completed, true
	case "publishing":
		return clubprofile.Completed, true
	case "poll_creating", "unknown":
		return clubprofile.Unknown, false
	case "failed":
		return clubprofile.Failed, false
	case "cancelled", "planned":
		return clubprofile.Cancelled, false
	default:
		return clubprofile.Failed, false
	}
}

func finishLegacyProfilePoll(ctx context.Context, tx *sql.Tx, p clubprofile.Poll, counts []int) error {
	if p.LegacyID == 0 {
		return nil
	}
	for i, n := range counts {
		if _, err := tx.ExecContext(ctx, "UPDATE movie_poll_options SET votes=? WHERE round_id=? AND position=?", n, p.LegacyID, i); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, "UPDATE movie_rounds SET state='cancelled',error_code='profile_collected' WHERE id=?", p.LegacyID)
	return err
}

// Immutable shape of the pre-profile storage format; not a second runtime model.
type legacyProfileRound struct {
	ID, ChatID, SlotAt, OpenedAt, ClosesAt, NextAttempt int64
	Feature, State, PollID, ErrorCode                   string
	PollMessageID                                       int
}

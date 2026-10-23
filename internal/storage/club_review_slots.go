package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/vaporon4a/movie-helper/internal/clubprofile"
)

func (s *Store) ReserveProfileSlot(ctx context.Context, sc clubprofile.Schedule, q clubprofile.Question, slot, now time.Time) error {
	if err := q.Validate(); err != nil {
		return err
	}
	return s.transaction(ctx, nil, func(tx *sql.Tx) error {
		loc, err := time.LoadLocation(sc.Zone)
		if err != nil {
			return err
		}
		occurrence := fmt.Sprintf("schedule:%d:%s", sc.ID, slot.In(loc).Format(time.DateOnly))
		if err := validateProfileSlot(ctx, tx, sc, occurrence); err != nil {
			return err
		}
		research, err := profileSlotRole(ctx, tx, sc, slot)
		if err != nil {
			return err
		}
		var session int64
		questions := []clubprofile.Question{q}
		if research {
			session, questions, err = researchSlotQuestions(ctx, tx, sc, slot, now)
			if err != nil {
				return err
			}
			if len(questions) == 0 {
				questions = []clubprofile.Question{q}
			}
		}
		return insertProfileBatch(ctx, tx, sc.ChatID, session, occurrence, questions, slot)
	})
}

func profileSlotRole(ctx context.Context, tx *sql.Tx, sc clubprofile.Schedule, slot time.Time) (bool, error) {
	week, err := clubprofile.WeekKey(slot, sc.Zone)
	if err != nil {
		return false, err
	}
	var count int
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM club_slot_roles WHERE chat_id=? AND week=?", sc.ChatID, week).Scan(&count); err != nil {
		return false, err
	}
	if count == 0 {
		if err = freezeProfileRoles(ctx, tx, sc.ChatID, week); err != nil {
			return false, err
		}
	}
	var role string
	err = tx.QueryRowContext(ctx, "SELECT role FROM club_slot_roles WHERE chat_id=? AND week=? AND schedule_id=?", sc.ChatID, week, sc.ID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	} // Newly added mid-week slots remain ordinary.
	return role == "research", err
}

func freezeProfileRoles(ctx context.Context, tx *sql.Tx, chat int64, week string) error {
	rows, err := tx.QueryContext(ctx, "SELECT id,topic,weekday,clock,enabled FROM club_profile_schedules WHERE chat_id=? AND enabled=1", chat)
	if err != nil {
		return err
	}
	defer rows.Close()
	var schedules []clubprofile.Schedule
	for rows.Next() {
		var sc clubprofile.Schedule
		if err = rows.Scan(&sc.ID, &sc.Topic, &sc.Weekday, &sc.Clock, &sc.Enabled); err != nil {
			return err
		}
		schedules = append(schedules, sc)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	roles := clubprofile.ResearchRoles(schedules)
	for _, sc := range schedules {
		role := "ordinary"
		if roles[sc.ID] {
			role = "research"
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO club_slot_roles(chat_id,week,schedule_id,role) VALUES(?,?,?,?)", chat, week, sc.ID, role); err != nil {
			return err
		}
	}
	return nil
}

func researchSlotQuestions(ctx context.Context, tx *sql.Tx, sc clubprofile.Schedule, slot, now time.Time) (int64, []clubprofile.Question, error) {
	if err := advanceChatReviews(ctx, tx, sc.ChatID, now); err != nil {
		return 0, nil, err
	}
	session, err := nextReviewSession(ctx, tx, sc.ChatID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil, nil
	}
	if err != nil {
		return 0, nil, err
	}
	polls, err := reviewPolls(ctx, tx, session.ID)
	if err != nil {
		return 0, nil, err
	}
	used, err := researchDayUsed(ctx, tx, sc.ChatID, sc.Zone, slot)
	if err != nil || used {
		return 0, nil, err
	}
	if len(polls) == 0 {
		if err = freezeResearchPlan(ctx, tx, &session); err != nil {
			return 0, nil, err
		}
	}
	d := clubprofile.ReviewProgress(session, polls, now)
	if len(d.Next) == 0 {
		return 0, nil, nil
	}
	var title string
	if err = tx.QueryRowContext(ctx, "SELECT f.title FROM club_watches w JOIN club_films f ON f.tmdb_id=w.tmdb_id WHERE w.id=? AND w.state='active'", session.WatchID).Scan(&title); err != nil {
		return 0, nil, err
	}
	questions := make([]clubprofile.Question, 0, len(d.Next))
	for _, aspect := range d.Next {
		q, e := session.Plan.QuestionFor(aspect, title)
		if e != nil {
			return 0, nil, e
		}
		q.Description = clubprofile.PollDescription(clubprofile.DefaultDuration)
		questions = append(questions, q)
	}
	_, err = tx.ExecContext(ctx, "UPDATE club_review_sessions SET state='researching',reason='questions_reserved' WHERE id=?", session.ID)
	return session.ID, questions, err
}

func insertProfileBatch(ctx context.Context, tx *sql.Tx, chat, session int64, occurrence string, questions []clubprofile.Question, slot time.Time) error {
	r, err := tx.ExecContext(ctx, "INSERT INTO club_poll_batches(chat_id,session_id,occurrence,slot_at) VALUES(?,NULLIF(?,0),?,?)", chat, session, occurrence, slot.Unix())
	if err != nil {
		return err
	}
	id, err := r.LastInsertId()
	if err != nil {
		return err
	}
	for i, q := range questions {
		if err = q.Validate(); err != nil {
			return err
		}
		raw, e := json.Marshal(q)
		if e != nil {
			return e
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO club_profile_polls(chat_id,topic,session_id,batch_id,position,occurrence,state,question_json,slot_at,closes_at,duration,next_attempt) VALUES(?,?,NULLIF(?,0),?,?,?,'planned',?,?,?,?,?)`, chat, q.Topic, session, id, i, occurrence, raw, slot.Unix(), slot.Add(clubprofile.DefaultDuration).Unix(), int64(clubprofile.DefaultDuration/time.Second), slot.Unix())
		if err != nil {
			return err
		}
	}
	return nil
}

func reviewPolls(ctx context.Context, tx *sql.Tx, session int64) ([]clubprofile.Poll, error) {
	rows, err := tx.QueryContext(ctx, "SELECT "+profilePollColumns+" FROM club_profile_polls WHERE session_id=? ORDER BY id", session)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var polls []clubprofile.Poll
	for rows.Next() {
		p, e := scanProfilePoll(rows)
		if e != nil {
			return nil, e
		}
		polls = append(polls, p)
	}
	return polls, rows.Err()
}

func advanceChatReviews(ctx context.Context, tx *sql.Tx, chat int64, now time.Time) error {
	rows, err := tx.QueryContext(ctx, "SELECT "+reviewSessionColumns+" FROM club_review_sessions WHERE (?=0 OR chat_id=?) AND state IN ('queued','researching','blocked') ORDER BY id", chat, chat)
	if err != nil {
		return err
	}
	defer rows.Close()
	var sessions []clubprofile.ReviewSession
	for rows.Next() {
		s, e := scanReviewSession(rows)
		if e != nil {
			return e
		}
		sessions = append(sessions, s)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	for _, s := range sessions {
		if err = advanceReview(ctx, tx, s, now); err != nil {
			return err
		}
	}
	return nil
}

func advanceReview(ctx context.Context, tx *sql.Tx, s clubprofile.ReviewSession, now time.Time) error {
	polls, err := reviewPolls(ctx, tx, s.ID)
	if err != nil {
		return err
	}
	d := clubprofile.ReviewProgress(s, polls, now)
	raw, err := json.Marshal(d.Coverage)
	if err != nil {
		return err
	}
	r, err := tx.ExecContext(ctx, "UPDATE club_review_sessions SET state=?,reason=?,coverage_json=? WHERE id=? AND (state<>? OR reason<>? OR coverage_json<>?)", d.State, d.Reason, raw, s.ID, d.State, d.Reason, raw)
	if err != nil {
		return err
	}
	changed, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if changed > 0 {
		if err = touchWatchCard(ctx, tx, s.WatchID); err != nil {
			return err
		}
	}
	if d.State == "partial" || d.State == "expired" || d.State == cancelledState {
		_, err = tx.ExecContext(ctx, "UPDATE club_profile_polls SET state='cancelled',error_code=? WHERE session_id=? AND state='planned'", d.Reason, s.ID)
	}
	return err
}

func touchWatchCard(ctx context.Context, tx *sql.Tx, watch int64) error {
	_, err := tx.ExecContext(ctx, "UPDATE club_watch_drafts SET version=version+1 WHERE watch_id=? AND state='saved'", watch)
	return err
}

func (s *Store) AdvanceProfileReviews(ctx context.Context, now time.Time) error {
	return s.transaction(ctx, nil, func(tx *sql.Tx) error { return advanceChatReviews(ctx, tx, 0, now) })
}

func validateProfileSlot(ctx context.Context, tx *sql.Tx, sc clubprofile.Schedule, occurrence string) error {
	var valid bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM club_profile_schedules s JOIN chats c ON c.chat_id=s.chat_id WHERE s.id=? AND s.chat_id=? AND s.enabled=1 AND c.active=1 AND s.effective=? AND c.zone=? AND s.topic=? AND s.weekday=? AND s.clock=?)`, sc.ID, sc.ChatID, sc.Effective.Unix(), sc.Zone, sc.Topic, sc.Weekday, sc.Clock).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return clubprofile.ErrConflict
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM club_poll_batches WHERE chat_id=? AND occurrence=?)", sc.ChatID, occurrence).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return clubprofile.ErrDuplicate
	}
	return nil
}

func researchDayUsed(ctx context.Context, tx *sql.Tx, chat int64, zone string, slot time.Time) (bool, error) {
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return false, err
	}
	local := slot.In(loc)
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	var used bool
	err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM club_poll_batches WHERE chat_id=? AND session_id IS NOT NULL AND slot_at>=? AND slot_at<?)", chat, start.Unix(), start.AddDate(0, 0, 1).Unix()).Scan(&used)
	return used, err
}

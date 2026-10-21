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

const profilePollColumns = `id,chat_id,COALESCE(session_id,0),COALESCE(batch_id,0),COALESCE(legacy_id,0),position,state,question_json,
 telegram_poll_id,message_id,slot_at,opened_at,closes_at,duration,next_attempt,error_code,result_json,cleanup_eligible,delivery_counted`

func scanProfilePoll(row interface{ Scan(...any) error }) (clubprofile.Poll, error) {
	var p clubprofile.Poll
	var question string
	var result sql.NullString
	var slot, opened, closes, duration, next int64
	err := row.Scan(&p.ID, &p.ChatID, &p.SessionID, &p.BatchID, &p.LegacyID, &p.Position, &p.State, &question, &p.TelegramID, &p.MessageID, &slot, &opened, &closes, &duration, &next, &p.ErrorCode, &result, &p.CleanupEligible, &p.DeliveryCounted)
	if err != nil {
		return p, err
	}
	if err = json.Unmarshal([]byte(question), &p.Question); err != nil {
		return p, err
	}
	if result.Valid {
		if err = json.Unmarshal([]byte(result.String), &p.Result); err != nil {
			return p, err
		}
	}
	p.SlotAt = time.Unix(slot, 0)
	p.OpenedAt = time.Unix(opened, 0)
	p.ClosesAt = time.Unix(closes, 0)
	p.NextAttempt = time.Unix(next, 0)
	p.Duration = time.Duration(duration) * time.Second
	return p, nil
}

func (s *Store) ProfilePolls(ctx context.Context, chat int64, state clubprofile.PollState) ([]clubprofile.Poll, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+profilePollColumns+" FROM club_profile_polls WHERE (?=0 OR chat_id=?) AND (?='' OR state=?) ORDER BY id", chat, chat, state, state)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []clubprofile.Poll
	for rows.Next() {
		p, e := scanProfilePoll(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) PlanProfilePoll(ctx context.Context, operation *int64, p clubprofile.Poll) (int64, error) {
	if err := p.Question.Validate(); err != nil {
		return 0, err
	}
	if p.ChatID == 0 || p.Duration < 5*time.Minute || p.Duration > clubprofile.DefaultDuration || p.Position < 0 || p.Position > 1 {
		return 0, clubprofile.ErrInvalid
	}
	question, err := json.Marshal(p.Question)
	if err != nil {
		return 0, err
	}
	var id int64
	err = s.transaction(ctx, operation, func(tx *sql.Tx) error {
		occurrence := fmt.Sprintf("slot:%s:%d", p.Question.Topic, p.SlotAt.Unix())
		if operation != nil {
			occurrence = fmt.Sprintf("manual:%d", *operation)
		}
		res, e := tx.ExecContext(ctx, `INSERT INTO club_profile_polls(chat_id,topic,session_id,batch_id,position,occurrence,state,question_json,slot_at,closes_at,duration,next_attempt)
 VALUES(?,?,NULLIF(?,0),NULLIF(?,0),?,?,'planned',?,?,?,?,?) ON CONFLICT(chat_id,occurrence,position) DO NOTHING`, p.ChatID, p.Question.Topic, p.SessionID, p.BatchID, p.Position, occurrence, question, p.SlotAt.Unix(), p.SlotAt.Add(p.Duration).Unix(), int64(p.Duration/time.Second), p.SlotAt.Unix())
		if e != nil {
			return e
		}
		n, e := res.RowsAffected()
		if e != nil {
			return e
		}
		if n == 0 {
			return clubprofile.ErrDuplicate
		}
		id, e = res.LastInsertId()
		return e
	})
	return id, err
}

func (s *Store) ClaimProfilePoll(ctx context.Context, id int64, from, to clubprofile.PollState) (bool, error) {
	if (from != clubprofile.Planned || to != clubprofile.Creating) && (from != clubprofile.Open || to != clubprofile.Closing) {
		return false, clubprofile.ErrInvalid
	}
	r, err := s.db.ExecContext(ctx, `UPDATE club_profile_polls SET state=? WHERE id=? AND state=? AND (state<>'planned' OR (
 EXISTS(SELECT 1 FROM chats WHERE chat_id=club_profile_polls.chat_id AND active=1)
 AND (session_id IS NULL OR EXISTS(SELECT 1 FROM club_review_sessions WHERE id=club_profile_polls.session_id AND state='researching'))
 AND NOT EXISTS(SELECT 1 FROM club_profile_polls sibling WHERE sibling.batch_id=club_profile_polls.batch_id AND sibling.position<club_profile_polls.position AND sibling.state IN ('planned','creating','unknown'))))`, to, id, from)
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	return n == 1, err
}

func (s *Store) OpenProfilePoll(ctx context.Context, id int64, pollID string, messageID int, now time.Time) error {
	if pollID == "" || messageID <= 0 {
		return clubprofile.ErrInvalid
	}
	r, e := s.db.ExecContext(ctx, `UPDATE club_profile_polls SET state='open',delivery_counted=1,telegram_poll_id=?,message_id=?,opened_at=?,closes_at=?+duration,error_code='' WHERE id=? AND state='creating'`, pollID, messageID, now.Unix(), now.Unix(), id)
	return profileChanged(r, e)
}

func profileChanged(r sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err == nil && n == 0 {
		return clubprofile.ErrConflict
	}
	return err
}

func (s *Store) DeferProfilePoll(ctx context.Context, id int64, from, to clubprofile.PollState, next time.Time, reason string) error {
	if !profileRetryTransition(from, to) {
		return clubprofile.ErrInvalid
	}
	r, e := s.db.ExecContext(ctx, "UPDATE club_profile_polls SET state=?,delivery_counted=MAX(delivery_counted,?='unknown'),next_attempt=?,error_code=? WHERE id=? AND state=?", to, to, next.Unix(), reason, id, from)
	return profileChanged(r, e)
}

func profileRetryTransition(from, to clubprofile.PollState) bool {
	if from == clubprofile.Creating {
		return to == clubprofile.Planned || to == clubprofile.Unknown || to == clubprofile.Failed
	}
	if from == clubprofile.Closing {
		return to == clubprofile.Open || to == clubprofile.Failed
	}
	return from == clubprofile.Planned && to == clubprofile.Cancelled
}

func (s *Store) CompleteProfilePoll(ctx context.Context, result clubprofile.PollResult) (bool, error) {
	var applied bool
	err := s.transaction(ctx, nil, func(tx *sql.Tx) error {
		var err error
		applied, err = completeProfilePoll(ctx, tx, result)
		return err
	})
	return applied && err == nil, err
}

func completeProfilePoll(ctx context.Context, tx *sql.Tx, result clubprofile.PollResult) (bool, error) {
	p, err := scanProfilePoll(tx.QueryRowContext(ctx, "SELECT "+profilePollColumns+" FROM club_profile_polls WHERE telegram_poll_id=?", result.PollID))
	if errors.Is(err, sql.ErrNoRows) {
		return false, clubprofile.ErrNotFound
	}
	if err != nil {
		return false, err
	}
	if p.State == clubprofile.Completed {
		return false, nil
	}
	if p.State != clubprofile.Open && p.State != clubprofile.Closing && p.State != clubprofile.Failed {
		return false, clubprofile.ErrConflict
	}
	if err = result.ValidateFinal(p.Question, p.TelegramID); err != nil {
		return false, err
	}
	data, err := json.Marshal(result)
	if err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE club_profile_polls SET state='completed',result_json=?,error_code='' WHERE id=?", data, p.ID); err != nil {
		return false, err
	}
	if err = finishLegacyProfilePoll(ctx, tx, p, result.Counts); err != nil {
		return false, err
	}
	if err = enqueueProfileCleanup(ctx, tx, p, result.ReceivedAt); err != nil {
		return false, err
	}
	if err = refreshProfileCoverage(ctx, tx, p.SessionID, result.ReceivedAt); err != nil {
		return false, err
	}
	return true, nil
}

func enqueueProfileCleanup(ctx context.Context, tx *sql.Tx, p clubprofile.Poll, now time.Time) error {
	if !p.CleanupEligible || p.MessageID <= 0 {
		return nil
	}
	deadline := p.OpenedAt.Add(47 * time.Hour)
	state := clubprofile.CleanupPending
	reason := ""
	if !now.Before(deadline) {
		state = clubprofile.CleanupExpired
		reason = "deadline"
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO club_poll_cleanup(poll_id,chat_id,message_id,state,next_attempt,deadline,reason)
 VALUES(?,?,?,?,?,?,?) ON CONFLICT(poll_id) DO NOTHING`, p.ID, p.ChatID, p.MessageID, state, now.Unix(), deadline.Unix(), reason)
	return err
}

func refreshProfileCoverage(ctx context.Context, tx *sql.Tx, sessionID int64, now time.Time) error {
	if sessionID == 0 {
		return nil
	}
	session, err := scanReviewSession(tx.QueryRowContext(ctx, "SELECT "+reviewSessionColumns+" FROM club_review_sessions WHERE id=?", sessionID))
	if err != nil {
		return err
	}
	return advanceReview(ctx, tx, session, now)
}

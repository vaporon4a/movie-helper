package storage

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/vaporon4a/movie-helper/internal/movieclub"
)

func (s *Store) SetMovieSchedule(ctx context.Context, op, chat int64, weekday int, clock string, enabled bool, now time.Time) error {
	if weekday < 0 || weekday > 6 {
		return errors.New("invalid weekday")
	}
	if _, err := time.Parse("15:04", clock); err != nil || len(clock) != 5 {
		return errors.New("invalid clock")
	}
	return s.transaction(ctx, &op, func(tx *sql.Tx) error {
		return setMovieSchedule(ctx, tx, chat, weekday, clock, enabled, now)
	})
}

func setMovieSchedule(ctx context.Context, tx *sql.Tx, chat int64, weekday int, clock string, enabled bool, now time.Time) error {
	var zone string
	var active bool
	if err := tx.QueryRowContext(ctx, "SELECT zone,active FROM chats WHERE chat_id=?", chat).Scan(&zone, &active); err != nil {
		return err
	}
	if enabled && (zone == "" || !active) {
		return errors.New("set timezone or reconnect chat first")
	}
	changed, err := movieScheduleChanged(ctx, tx, chat, weekday, clock, enabled)
	if err != nil {
		return err
	}
	if changed {
		if err = cancelPlannedWeekday(ctx, tx, chat, weekday, zone); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO movie_poll_schedules(chat_id,feature,weekday,clock,enabled,effective)
 VALUES(?,'genre',?,?,?,?) ON CONFLICT(chat_id,feature,weekday) DO UPDATE SET
 clock=CASE WHEN excluded.enabled THEN excluded.clock ELSE movie_poll_schedules.clock END,
 enabled=excluded.enabled,effective=excluded.effective`, chat, weekday, clock, enabled, now.Unix())
	return err
}

func movieScheduleChanged(ctx context.Context, tx *sql.Tx, chat int64, weekday int, clock string, enabled bool) (bool, error) {
	var oldClock string
	var oldEnabled bool
	err := tx.QueryRowContext(ctx, `SELECT clock,enabled FROM movie_poll_schedules
 WHERE chat_id=? AND feature='genre' AND weekday=?`, chat, weekday).Scan(&oldClock, &oldEnabled)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return oldClock != clock || oldEnabled != enabled, err
}

func (s *Store) PauseMovieSchedules(ctx context.Context, op, chat int64, now time.Time) error {
	return s.transaction(ctx, &op, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "UPDATE movie_rounds SET state='cancelled',error_code='schedule_paused' WHERE chat_id=? AND state='planned'", chat); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, "UPDATE movie_poll_schedules SET enabled=0,effective=? WHERE chat_id=? AND feature='genre'", now.Unix(), chat)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return movieclub.ErrConflict
		}
		return nil
	})
}

func cancelPlannedWeekday(ctx context.Context, tx *sql.Tx, chat int64, weekday int, zone string) error {
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return err
	}
	var ids []int64
	err = func() error {
		rows, queryErr := tx.QueryContext(ctx, "SELECT id,slot_at FROM movie_rounds WHERE chat_id=? AND state='planned'", chat)
		if queryErr != nil {
			return queryErr
		}
		defer rows.Close()
		for rows.Next() {
			var id, slot int64
			if scanErr := rows.Scan(&id, &slot); scanErr != nil {
				return scanErr
			}
			if int(time.Unix(slot, 0).In(loc).Weekday()) == weekday {
				ids = append(ids, id)
			}
		}
		return rows.Err()
	}()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err = tx.ExecContext(ctx, "UPDATE movie_rounds SET state='cancelled',error_code='schedule_changed' WHERE id=? AND state='planned'", id); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) MovieSchedules(ctx context.Context, chat int64) ([]movieclub.Schedule, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT p.id,p.chat_id,p.feature,p.weekday,p.clock,p.enabled AND c.active,p.effective,c.zone
 FROM movie_poll_schedules p JOIN chats c USING(chat_id)
 WHERE (?=0 OR p.chat_id=?) ORDER BY p.chat_id,p.feature,p.weekday`, chat, chat)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []movieclub.Schedule
	for rows.Next() {
		var value movieclub.Schedule
		if err = rows.Scan(&value.ID, &value.ChatID, &value.Feature, &value.Weekday, &value.Clock, &value.Enabled, &value.Effective, &value.Zone); err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, rows.Err()
}

func reserveMovieRound(ctx context.Context, tx *sql.Tx, feature movieclub.Feature, chat, slotAt, closesAt int64, options []movieclub.Option, skipIfActive bool) (int64, error) {
	if !feature.Valid() {
		return 0, movieclub.ErrUnknownFeature
	}
	var active int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM movie_rounds WHERE chat_id=? AND state IN
 ('planned','poll_creating','open','closing','selecting','ready','publishing','unknown')`, chat).Scan(&active); err != nil {
		return 0, err
	}
	if active != 0 {
		if skipIfActive {
			_, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO movie_rounds(chat_id,feature,slot_at,state,closes_at,error_code)
 VALUES(?,?,?,'cancelled',?,'active_round')`, chat, feature, slotAt, closesAt)
			return 0, err
		}
		return 0, movieclub.ErrActiveRound
	}
	result, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO movie_rounds(chat_id,feature,slot_at,state,closes_at,next_attempt)
 VALUES(?,?,?,'planned',?,?)`, chat, feature, slotAt, closesAt, slotAt)
	if err != nil {
		return 0, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, movieclub.ErrDuplicate
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	for position, option := range options {
		if _, err = tx.ExecContext(ctx, `INSERT INTO movie_poll_options(round_id,position,option_kind,provider_id,label)
 VALUES(?,?,?,?,?)`, id, position, option.Kind, option.ProviderID, option.Label); err != nil {
			return 0, err
		}
	}
	return id, nil
}

func (s *Store) ReserveMovieRound(ctx context.Context, feature movieclub.Feature, chat, slotAt, closesAt int64, options []movieclub.Option) (int64, error) {
	var id int64
	err := s.transaction(ctx, nil, func(tx *sql.Tx) error {
		var err error
		id, err = reserveMovieRound(ctx, tx, feature, chat, slotAt, closesAt, options, true)
		return err
	})
	return id, err
}

func (s *Store) StartMovieRound(ctx context.Context, feature movieclub.Feature, op, chat int64, at time.Time, duration time.Duration, options []movieclub.Option) (int64, error) {
	var id int64
	err := s.transaction(ctx, &op, func(tx *sql.Tx) error {
		var err error
		id, err = reserveMovieRound(ctx, tx, feature, chat, at.Unix(), at.Add(duration).Unix(), options, false)
		return err
	})
	return id, err
}

const movieRoundColumns = `id,chat_id,feature,slot_at,state,telegram_poll_id,poll_message_id,opened_at,closes_at,winner,result_text,next_attempt,error_code,publish_stage,page2_state`

func scanMovieRound(row interface{ Scan(...any) error }) (movieclub.Round, error) {
	var value movieclub.Round
	err := row.Scan(&value.ID, &value.ChatID, &value.Feature, &value.SlotAt, &value.State, &value.PollID,
		&value.PollMessageID, &value.OpenedAt, &value.ClosesAt, &value.Winner, &value.ResultText,
		&value.NextAttempt, &value.ErrorCode, &value.PublishStage, &value.Page2State)
	return value, err
}

func (s *Store) MovieRound(ctx context.Context, chat, id int64) (movieclub.Round, error) {
	round, err := scanMovieRound(s.db.QueryRowContext(ctx, "SELECT "+movieRoundColumns+" FROM movie_rounds WHERE id=? AND (?=0 OR chat_id=?)", id, chat, chat))
	if err != nil {
		return round, err
	}
	round.Options, err = s.MovieOptions(ctx, round.ID)
	return round, err
}

func (s *Store) MovieRounds(ctx context.Context, state movieclub.State) ([]movieclub.Round, error) {
	if !state.Valid() {
		return nil, errors.New("invalid movie state")
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+movieRoundColumns+" FROM movie_rounds WHERE state=? ORDER BY next_attempt,id", state)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []movieclub.Round
	for rows.Next() {
		value, scanErr := scanMovieRound(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, value)
	}
	return out, rows.Err()
}

func (s *Store) LatestMovieRounds(ctx context.Context, chat int64) ([]movieclub.Round, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+movieRoundColumns+` FROM movie_rounds
 WHERE chat_id=? ORDER BY id DESC LIMIT 5`, chat)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []movieclub.Round
	for rows.Next() {
		value, scanErr := scanMovieRound(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, value)
	}
	return out, rows.Err()
}

func (s *Store) MovieOptions(ctx context.Context, round int64) ([]movieclub.Option, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT round_id,position,option_kind,provider_id,label,votes
 FROM movie_poll_options WHERE round_id=? ORDER BY position`, round)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []movieclub.Option
	for rows.Next() {
		var value movieclub.Option
		if err = rows.Scan(&value.RoundID, &value.Position, &value.Kind, &value.ProviderID, &value.Label, &value.Votes); err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, rows.Err()
}

func (s *Store) ClaimMovieRound(ctx context.Context, id int64, from, to movieclub.State, now time.Time) (bool, error) {
	if !movieclub.CanTransition(from, to) {
		return false, errors.New("invalid movie transition")
	}
	result, err := s.db.ExecContext(ctx, "UPDATE movie_rounds SET state=? WHERE id=? AND state=? AND next_attempt<=?", to, id, from, now.Unix())
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

func (s *Store) OpenMovieRound(ctx context.Context, id int64, pollID string, messageID int, opened, closes time.Time) error {
	result, err := s.db.ExecContext(ctx, `UPDATE movie_rounds SET state='open',telegram_poll_id=?,poll_message_id=?,opened_at=?,closes_at=?,next_attempt=0,error_code=''
 WHERE id=? AND state='poll_creating'`, pollID, messageID, opened.Unix(), closes.Unix(), id)
	return changed(result, err)
}

func (s *Store) DeferMovieRound(ctx context.Context, id int64, from, to movieclub.State, next time.Time, code string) error {
	if !movieclub.CanTransition(from, to) || strings.ContainsAny(code, "\r\n") || len(code) > 80 {
		return errors.New("invalid movie transition")
	}
	result, err := s.db.ExecContext(ctx, "UPDATE movie_rounds SET state=?,next_attempt=?,error_code=? WHERE id=? AND state=?", to, next.Unix(), code, id, from)
	return changed(result, err)
}

func (s *Store) SaveMoviePoll(ctx context.Context, id int64, votes []int) error {
	return s.transaction(ctx, nil, func(tx *sql.Tx) error {
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM movie_poll_options WHERE round_id=?", id).Scan(&count); err != nil {
			return err
		}
		if count != len(votes) {
			return errors.New("poll option count changed")
		}
		for position, vote := range votes {
			if vote < 0 {
				return errors.New("invalid vote count")
			}
			if _, err := tx.ExecContext(ctx, "UPDATE movie_poll_options SET votes=? WHERE round_id=? AND position=?", vote, id, position); err != nil {
				return err
			}
		}
		result, err := tx.ExecContext(ctx, "UPDATE movie_rounds SET state='selecting',next_attempt=0,error_code='' WHERE id=? AND state IN ('open','closing')", id)
		return changed(result, err)
	})
}

func (s *Store) SaveMoviePollByID(ctx context.Context, pollID string, votes []int) error {
	var id int64
	if err := s.db.QueryRowContext(ctx, "SELECT id FROM movie_rounds WHERE telegram_poll_id=?", pollID).Scan(&id); err != nil {
		return err
	}
	return s.SaveMoviePoll(ctx, id, votes)
}

func (s *Store) SaveMovieSelection(ctx context.Context, id int64, winner string, movies []movieclub.Recommendation) error {
	return s.transaction(ctx, nil, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM movie_recommendations WHERE round_id=?", id); err != nil {
			return err
		}
		for _, movie := range movies {
			if _, err := tx.ExecContext(ctx, `INSERT INTO movie_recommendations(round_id,page,position,relation,tmdb_id,title,release_year,overview,poster_path,rating,vote_count,popularity)
 VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, id, movie.Page, movie.Position, movie.Relation, movie.ID, movie.Title, movie.Year,
				movie.Overview, movie.PosterPath, movie.Rating, movie.VoteCount, movie.Popularity); err != nil {
				return err
			}
		}
		page2 := "none"
		if len(movies) > 10 {
			page2 = "ready"
		}
		result, err := tx.ExecContext(ctx, `UPDATE movie_rounds SET state='ready',winner=?,result_text='',page2_state=?,next_attempt=0,error_code=''
 WHERE id=? AND state='selecting'`, winner, page2, id)
		return changed(result, err)
	})
}

func (s *Store) MovieRecommendations(ctx context.Context, round int64, page int) ([]movieclub.Recommendation, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT round_id,page,position,relation,tmdb_id,title,release_year,overview,poster_path,rating,vote_count,popularity
 FROM movie_recommendations WHERE round_id=? AND page=? ORDER BY position`, round, page)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []movieclub.Recommendation
	for rows.Next() {
		var value movieclub.Recommendation
		if err = rows.Scan(&value.RoundID, &value.Page, &value.Position, &value.Relation, &value.ID, &value.Title, &value.Year,
			&value.Overview, &value.PosterPath, &value.Rating, &value.VoteCount, &value.Popularity); err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, rows.Err()
}

func (s *Store) RecentMovieIDs(ctx context.Context, chat int64, since time.Time) (map[int64]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT m.tmdb_id FROM movie_recommendations m JOIN movie_rounds r ON r.id=m.round_id
 WHERE r.chat_id=? AND r.state='published' AND r.slot_at>=?`, chat, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64]bool)
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

func (s *Store) SetMoviePublishStage(ctx context.Context, id int64, stage int, state movieclub.State) error {
	if stage < 0 || stage > 2 || !movieclub.CanTransition(movieclub.StatePublishing, state) {
		return errors.New("invalid publish stage")
	}
	result, err := s.db.ExecContext(ctx, "UPDATE movie_rounds SET publish_stage=?,state=?,next_attempt=0,error_code='' WHERE id=? AND state='publishing'", stage, state, id)
	return changed(result, err)
}

func (s *Store) ClaimMoviePage2(ctx context.Context, chat, id int64) (bool, error) {
	result, err := s.db.ExecContext(ctx, "UPDATE movie_rounds SET page2_state='sending' WHERE id=? AND chat_id=? AND state='published' AND page2_state='ready'", id, chat)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

func (s *Store) FinishMoviePage2(ctx context.Context, chat, id int64, state string) error {
	if state != "ready" && state != "sent" && state != "unknown" {
		return errors.New("invalid page state")
	}
	result, err := s.db.ExecContext(ctx, "UPDATE movie_rounds SET page2_state=? WHERE id=? AND chat_id=? AND page2_state='sending'", state, id, chat)
	return changed(result, err)
}

func (s *Store) ResolveMovieRound(ctx context.Context, op, chat, id int64, action movieclub.ResolveAction) error {
	return s.transaction(ctx, &op, func(tx *sql.Tx) error {
		handled, err := resolveMoviePage2(ctx, tx, chat, id, action)
		if err != nil || handled {
			return err
		}
		target := movieclub.StateCancelled
		if action == movieclub.ResolveSent {
			target = movieclub.StatePublished
		} else if action == movieclub.ResolveRetry {
			target = movieclub.StateReady
		} else if action != movieclub.ResolveCancel {
			return errors.New("invalid resolution")
		}
		result, err := tx.ExecContext(ctx, "UPDATE movie_rounds SET state=?,error_code='' WHERE id=? AND chat_id=? AND state='unknown'", target, id, chat)
		return changed(result, err)
	})
}

func resolveMoviePage2(ctx context.Context, tx *sql.Tx, chat, id int64, action movieclub.ResolveAction) (bool, error) {
	if action != movieclub.ResolveSent && action != movieclub.ResolveRetry {
		return false, nil
	}
	var page2 string
	if err := tx.QueryRowContext(ctx, "SELECT page2_state FROM movie_rounds WHERE id=? AND chat_id=?", id, chat).Scan(&page2); err != nil {
		return false, err
	}
	if page2 != string(movieclub.DeliveryUnknown) {
		return false, nil
	}
	target := movieclub.ResolveSent
	if action == movieclub.ResolveRetry {
		target = "ready"
	}
	result, err := tx.ExecContext(ctx, "UPDATE movie_rounds SET page2_state=? WHERE id=? AND chat_id=? AND page2_state='unknown'", target, id, chat)
	return true, changed(result, err)
}

func (s *Store) DisableMovieSchedules(ctx context.Context, chat int64) error {
	_, err := s.db.ExecContext(ctx, "UPDATE movie_poll_schedules SET enabled=0 WHERE chat_id=?", chat)
	return err
}

func (s *Store) RecoverMovieRounds(ctx context.Context) error {
	return s.transaction(ctx, nil, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE movie_rounds SET state='unknown',error_code='restart_during_network'
 WHERE state IN ('poll_creating','closing','publishing')`); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "UPDATE movie_rounds SET page2_state='unknown' WHERE page2_state='sending'")
		return err
	})
}

func changed(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return movieclub.ErrConflict
	}
	return nil
}

package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/vaporon4a/movie-helper/internal/featurevote"
)

const featureIdeaColumns = "id,chat_id,author_id,body,title,body_hash,state,created_at,updated_at"

func scanFeatureIdea(row interface{ Scan(...any) error }) (featurevote.Idea, error) {
	var idea featurevote.Idea
	err := row.Scan(&idea.ID, &idea.ChatID, &idea.AuthorID, &idea.Text, &idea.Title, &idea.Hash, &idea.State, &idea.CreatedAt, &idea.UpdatedAt)
	return idea, err
}

func (s *Store) AddFeature(ctx context.Context, op, chat, author int64, text, hash string, now time.Time) (featurevote.Idea, error) {
	var idea featurevote.Idea
	err := s.transaction(ctx, &op, func(tx *sql.Tx) error {
		var recent int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM feature_requests
 WHERE chat_id=? AND author_id=? AND created_at>=?`, chat, author, now.Add(-7*24*time.Hour).Unix()).Scan(&recent); err != nil {
			return err
		}
		if recent >= featurevote.MaxIdeasPerWeek {
			return featurevote.ErrRateLimit
		}
		result, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO feature_requests(chat_id,author_id,body,body_hash,created_at,updated_at)
 VALUES(?,?,?,?,?,?)`, chat, author, text, hash, now.Unix(), now.Unix())
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return featurevote.ErrDuplicate
		}
		idea.ID, err = result.LastInsertId()
		if err != nil {
			return err
		}
		idea = featurevote.Idea{ID: idea.ID, ChatID: chat, AuthorID: author, Text: text, Hash: hash, State: featurevote.IdeaActive, CreatedAt: now.Unix(), UpdatedAt: now.Unix()}
		return nil
	})
	return idea, err
}

func (s *Store) AuthorFeatures(ctx context.Context, chat, author int64) ([]featurevote.Idea, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+featureIdeaColumns+" FROM feature_requests WHERE chat_id=? AND author_id=? AND state='active' ORDER BY id", chat, author)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFeatureIdeas(rows)
}

func (s *Store) FeatureBacklog(ctx context.Context, chat int64) ([]featurevote.Idea, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+featureIdeaColumns+" FROM feature_requests WHERE chat_id=? AND state='backlog' ORDER BY updated_at,id", chat)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFeatureIdeas(rows)
}

func scanFeatureIdeas(rows *sql.Rows) ([]featurevote.Idea, error) {
	var ideas []featurevote.Idea
	for rows.Next() {
		idea, err := scanFeatureIdea(rows)
		if err != nil {
			return nil, err
		}
		ideas = append(ideas, idea)
	}
	return ideas, rows.Err()
}

func (s *Store) ChangeFeatureState(ctx context.Context, op, chat, id, actor int64, state featurevote.IdeaState, admin bool, now time.Time) error {
	return s.transaction(ctx, &op, func(tx *sql.Tx) error {
		var author int64
		var current featurevote.IdeaState
		if err := tx.QueryRowContext(ctx, "SELECT author_id,state FROM feature_requests WHERE id=? AND chat_id=?", id, chat).Scan(&author, &current); err != nil {
			return err
		}
		if !admin && (author != actor || current != featurevote.IdeaActive || state != featurevote.IdeaRemoved) {
			return featurevote.ErrConflict
		}
		var inRound int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM feature_round_options o JOIN feature_rounds r ON r.id=o.round_id
 WHERE o.idea_id=? AND r.state IN ('planned','opening','open','closing','ready','publishing','unknown')`, id).Scan(&inRound); err != nil {
			return err
		}
		if inRound != 0 {
			return featurevote.ErrConflict
		}
		result, err := tx.ExecContext(ctx, "UPDATE feature_requests SET state=?,updated_at=? WHERE id=? AND chat_id=?", state, now.Unix(), id, chat)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil || n != 1 {
			return featurevote.ErrConflict
		}
		return nil
	})
}

func (s *Store) SetFeatureSchedule(ctx context.Context, op, chat int64, weekday int, clock string, enabled bool, now time.Time) error {
	if weekday < 0 || weekday > 6 {
		return errors.New("invalid weekday")
	}
	if _, err := time.Parse("15:04", clock); err != nil || len(clock) != 5 {
		return errors.New("invalid clock")
	}
	return s.transaction(ctx, &op, func(tx *sql.Tx) error {
		var zone string
		var active bool
		if err := tx.QueryRowContext(ctx, "SELECT zone,active FROM chats WHERE chat_id=?", chat).Scan(&zone, &active); err != nil {
			return err
		}
		if enabled && (zone == "" || !active) {
			return errors.New("set timezone or reconnect chat first")
		}
		if _, err := tx.ExecContext(ctx, "UPDATE feature_rounds SET state='cancelled',error_code='schedule_changed' WHERE chat_id=? AND state='planned' AND parent_id IS NULL", chat); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO feature_vote_schedules(chat_id,weekday,clock,enabled,effective)
 VALUES(?,?,?,?,?) ON CONFLICT(chat_id) DO UPDATE SET weekday=excluded.weekday,
 clock=CASE WHEN excluded.enabled THEN excluded.clock ELSE feature_vote_schedules.clock END,
 enabled=excluded.enabled,effective=excluded.effective`, chat, weekday, clock, enabled, now.Unix())
		return err
	})
}

func (s *Store) PauseFeatureSchedule(ctx context.Context, op, chat int64, now time.Time) error {
	return s.transaction(ctx, &op, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "UPDATE feature_rounds SET state='cancelled',error_code='schedule_paused' WHERE chat_id=? AND state='planned' AND parent_id IS NULL", chat); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, "UPDATE feature_vote_schedules SET enabled=0,effective=? WHERE chat_id=?", now.Unix(), chat)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil || n != 1 {
			return featurevote.ErrConflict
		}
		return nil
	})
}

func (s *Store) FeatureSchedules(ctx context.Context, chat int64) ([]featurevote.Schedule, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT f.chat_id,f.weekday,f.clock,f.enabled AND c.active,f.effective,c.zone
 FROM feature_vote_schedules f JOIN chats c USING(chat_id) WHERE (?=0 OR f.chat_id=?) ORDER BY f.chat_id`, chat, chat)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var schedules []featurevote.Schedule
	for rows.Next() {
		var schedule featurevote.Schedule
		if err = rows.Scan(&schedule.ChatID, &schedule.Weekday, &schedule.Clock, &schedule.Enabled, &schedule.Effective, &schedule.Zone); err != nil {
			return nil, err
		}
		schedules = append(schedules, schedule)
	}
	return schedules, rows.Err()
}

func (s *Store) FeatureSettings(ctx context.Context, chat int64) (featurevote.SettingsView, error) {
	var view featurevote.SettingsView
	schedules, err := s.FeatureSchedules(ctx, chat)
	if err != nil {
		return view, err
	}
	if len(schedules) > 0 {
		view.Schedule, view.Configured = schedules[0], true
	}
	if err = s.db.QueryRowContext(ctx, "SELECT count(*) FROM feature_requests WHERE chat_id=? AND state='active'", chat).Scan(&view.Active); err != nil {
		return view, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+featureRoundColumns+" FROM feature_rounds WHERE chat_id=? ORDER BY id DESC LIMIT 5", chat)
	if err != nil {
		return view, err
	}
	defer rows.Close()
	for rows.Next() {
		round, scanErr := scanFeatureRound(rows)
		if scanErr != nil {
			return view, scanErr
		}
		view.Latest = append(view.Latest, round)
	}
	return view, rows.Err()
}

func reserveFeatureRound(ctx context.Context, tx *sql.Tx, chat, slot, closes int64, token string) (int64, error) {
	var duplicate int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM feature_rounds WHERE chat_id=? AND slot_at=?", chat, slot).Scan(&duplicate); err != nil {
		return 0, err
	}
	if duplicate != 0 {
		return 0, featurevote.ErrDuplicate
	}
	var active int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM feature_rounds WHERE chat_id=? AND state IN
 ('planned','opening','open','closing','ready','publishing','unknown')`, chat).Scan(&active); err != nil {
		return 0, err
	}
	if active != 0 {
		return 0, featurevote.ErrActiveRound
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO feature_rounds(chat_id,slot_at,token,state,closes_at,next_attempt)
 VALUES(?,?,?,'planned',?,?)`, chat, slot, token, closes, slot)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (s *Store) StartFeatureRound(ctx context.Context, op, chat int64, at time.Time, duration time.Duration, token string) (int64, error) {
	var id int64
	err := s.transaction(ctx, &op, func(tx *sql.Tx) error {
		var activeIdeas int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM feature_requests WHERE chat_id=? AND state='active'", chat).Scan(&activeIdeas); err != nil {
			return err
		}
		if activeIdeas == 0 {
			return featurevote.ErrNotFound
		}
		var err error
		id, err = reserveFeatureRound(ctx, tx, chat, at.Unix(), at.Add(duration).Unix(), token)
		return err
	})
	return id, err
}

func (s *Store) ReserveFeatureRound(ctx context.Context, chat, slot, closes int64, token string) (int64, error) {
	var id int64
	err := s.transaction(ctx, nil, func(tx *sql.Tx) error {
		var err error
		id, err = reserveFeatureRound(ctx, tx, chat, slot, closes, token)
		return err
	})
	return id, err
}

const featureRoundColumns = "id,chat_id,slot_at,token,state,message_id,opened_at,closes_at,next_attempt,COALESCE(winner_id,0),COALESCE(parent_id,0),COALESCE(runoff_id,0),outcome,error_code"

func scanFeatureRound(row interface{ Scan(...any) error }) (featurevote.Round, error) {
	var round featurevote.Round
	err := row.Scan(&round.ID, &round.ChatID, &round.SlotAt, &round.Token, &round.State, &round.MessageID, &round.OpenedAt,
		&round.ClosesAt, &round.NextAttempt, &round.WinnerID, &round.ParentID, &round.RunoffID, &round.Outcome, &round.ErrorCode)
	return round, err
}

func (s *Store) FeatureRounds(ctx context.Context, state featurevote.RoundState) ([]featurevote.Round, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+featureRoundColumns+" FROM feature_rounds WHERE state=? ORDER BY next_attempt,id", state)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var rounds []featurevote.Round
	for rows.Next() {
		round, scanErr := scanFeatureRound(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		rounds = append(rounds, round)
	}
	return rounds, rows.Err()
}

func (s *Store) FeatureRound(ctx context.Context, id int64) (featurevote.Round, error) {
	return scanFeatureRound(s.db.QueryRowContext(ctx, "SELECT "+featureRoundColumns+" FROM feature_rounds WHERE id=?", id))
}

func (s *Store) FeatureRoundOptions(ctx context.Context, roundID int64) ([]featurevote.Option, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT round_id,idea_id,position,votes,title,body
 FROM feature_round_options WHERE round_id=? ORDER BY position`, roundID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var options []featurevote.Option
	for rows.Next() {
		var option featurevote.Option
		if err = rows.Scan(&option.RoundID, &option.IdeaID, &option.Position, &option.Votes, &option.Title, &option.Text); err != nil {
			return nil, err
		}
		options = append(options, option)
	}
	return options, rows.Err()
}

func (s *Store) ActiveFeatures(ctx context.Context, chat int64) ([]featurevote.Idea, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+featureIdeaColumns+" FROM feature_requests WHERE chat_id=? AND state='active' ORDER BY id", chat)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFeatureIdeas(rows)
}

func (s *Store) SaveFeatureTitle(ctx context.Context, id int64, title string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE feature_requests SET title=? WHERE id=? AND title=''", title, id)
	return err
}

func (s *Store) SaveFeatureRoundOptions(ctx context.Context, roundID int64, options []featurevote.Option) error {
	return s.transaction(ctx, nil, func(tx *sql.Tx) error {
		var state featurevote.RoundState
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT state,(SELECT count(*) FROM feature_round_options WHERE round_id=?)
 FROM feature_rounds WHERE id=?`, roundID, roundID).Scan(&state, &count); err != nil {
			return err
		}
		if state != featurevote.RoundPlanned || count != 0 {
			return featurevote.ErrConflict
		}
		for position, option := range options {
			if _, err := tx.ExecContext(ctx, `INSERT INTO feature_round_options(round_id,position,idea_id,title,body)
 VALUES(?,?,?,?,?)`, roundID, position, option.IdeaID, option.Title, option.Text); err != nil {
				return err
			}
		}
		return nil
	})
}

func validFeatureTransition(from, to featurevote.RoundState) bool {
	allowed := map[featurevote.RoundState]featurevote.RoundState{
		featurevote.RoundPlanned: featurevote.RoundOpening,
		featurevote.RoundOpen:    featurevote.RoundClosing,
		featurevote.RoundReady:   featurevote.RoundPublishing,
	}
	return allowed[from] == to
}

func (s *Store) ClaimFeatureRound(ctx context.Context, id int64, from, to featurevote.RoundState, now time.Time) (bool, error) {
	if !validFeatureTransition(from, to) {
		return false, featurevote.ErrConflict
	}
	result, err := s.db.ExecContext(ctx, "UPDATE feature_rounds SET state=? WHERE id=? AND state=? AND next_attempt<=?", to, id, from, now.Unix())
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

func (s *Store) OpenFeatureRound(ctx context.Context, id int64, messageID int, now time.Time) error {
	result, err := s.db.ExecContext(ctx, `UPDATE feature_rounds SET state='open',message_id=?,opened_at=?,next_attempt=0,error_code=''
 WHERE id=? AND state='opening'`, messageID, now.Unix(), id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return featurevote.ErrConflict
	}
	return nil
}

func (s *Store) CancelEmptyFeatureRound(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, "UPDATE feature_rounds SET state='cancelled',error_code='no_active_ideas' WHERE id=? AND state='planned'", id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return featurevote.ErrConflict
	}
	return nil
}

func (s *Store) FeatureView(ctx context.Context, token string, user int64) (featurevote.View, error) {
	var view featurevote.View
	round, err := scanFeatureRound(s.db.QueryRowContext(ctx, "SELECT "+featureRoundColumns+" FROM feature_rounds WHERE token=?", token))
	if errors.Is(err, sql.ErrNoRows) {
		return view, featurevote.ErrNotFound
	}
	if err != nil {
		return view, err
	}
	view.Round = round
	view.Options, err = s.FeatureRoundOptions(ctx, round.ID)
	if err != nil {
		return view, err
	}
	err = s.db.QueryRowContext(ctx, "SELECT idea_id FROM feature_votes WHERE round_id=? AND user_id=?", round.ID, user).Scan(&view.SelectedID)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return view, err
}

func (s *Store) VoteFeature(ctx context.Context, token string, user, ideaID int64, now time.Time) error {
	return s.transaction(ctx, nil, func(tx *sql.Tx) error {
		var roundID, closes int64
		var state featurevote.RoundState
		if err := tx.QueryRowContext(ctx, "SELECT id,state,closes_at FROM feature_rounds WHERE token=?", token).Scan(&roundID, &state, &closes); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return featurevote.ErrNotFound
			}
			return err
		}
		if state != featurevote.RoundOpen || closes <= now.Unix() {
			return featurevote.ErrClosed
		}
		var exists bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM feature_round_options WHERE round_id=? AND idea_id=?)", roundID, ideaID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return featurevote.ErrConflict
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO feature_votes(round_id,user_id,idea_id,updated_at) VALUES(?,?,?,?)
 ON CONFLICT(round_id,user_id) DO UPDATE SET idea_id=excluded.idea_id,updated_at=excluded.updated_at`, roundID, user, ideaID, now.Unix())
		return err
	})
}

func (s *Store) FinalizeFeatureRound(ctx context.Context, id int64, runoffToken string, now time.Time) (featurevote.Round, error) {
	var finalized featurevote.Round
	err := s.transaction(ctx, nil, func(tx *sql.Tx) error {
		round, options, leaders, total, loadErr := loadFeatureTally(ctx, tx, id)
		if loadErr != nil {
			return loadErr
		}
		if persistErr := persistFeatureTally(ctx, tx, id, options); persistErr != nil {
			return persistErr
		}
		if outcomeErr := applyFeatureOutcome(ctx, tx, &round, leaders, total, runoffToken, now); outcomeErr != nil {
			return outcomeErr
		}
		round.Options, round.State = options, featurevote.RoundReady
		finalized = round
		return nil
	})
	return finalized, err
}

func loadFeatureTally(ctx context.Context, tx *sql.Tx, id int64) (featurevote.Round, []featurevote.Option, []featurevote.Option, int, error) {
	round, err := scanFeatureRound(tx.QueryRowContext(ctx, "SELECT "+featureRoundColumns+" FROM feature_rounds WHERE id=?", id))
	if err != nil {
		return round, nil, nil, 0, err
	}
	if round.State != featurevote.RoundClosing {
		return round, nil, nil, 0, featurevote.ErrConflict
	}
	options, err := featureOptionsTx(ctx, tx, id)
	if err != nil {
		return round, nil, nil, 0, err
	}
	leaders, total, err := tallyFeatureOptions(ctx, tx, options)
	return round, options, leaders, total, err
}

func persistFeatureTally(ctx context.Context, tx *sql.Tx, id int64, options []featurevote.Option) error {
	for _, option := range options {
		if _, err := tx.ExecContext(ctx, "UPDATE feature_round_options SET votes=? WHERE round_id=? AND idea_id=?", option.Votes, id, option.IdeaID); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, "DELETE FROM feature_votes WHERE round_id=?", id)
	return err
}

func applyFeatureOutcome(ctx context.Context, tx *sql.Tx, round *featurevote.Round, leaders []featurevote.Option, total int, runoffToken string, now time.Time) error {
	round.Outcome = "no_votes"
	if total == 0 {
		return setFeatureRoundReady(ctx, tx, *round, featurevote.RoundClosing)
	}
	if len(leaders) == 1 {
		round.Outcome, round.WinnerID = "winner", leaders[0].IdeaID
		if err := moveFeatureWinner(ctx, tx, *round, now); err != nil {
			return err
		}
		return setFeatureRoundReady(ctx, tx, *round, featurevote.RoundClosing)
	}
	if round.ParentID != 0 {
		round.Outcome = "tie_final"
		return setFeatureRoundReady(ctx, tx, *round, featurevote.RoundClosing)
	}
	round.Outcome = "tie"
	if err := setFeatureRoundReady(ctx, tx, *round, featurevote.RoundClosing); err != nil {
		return err
	}
	runoffID, err := createFeatureRunoff(ctx, tx, *round, leaders, runoffToken, now)
	round.RunoffID = runoffID
	if err != nil {
		return err
	}
	return setFeatureRoundReady(ctx, tx, *round, featurevote.RoundReady)
}

func moveFeatureWinner(ctx context.Context, tx *sql.Tx, round featurevote.Round, now time.Time) error {
	result, err := tx.ExecContext(ctx, "UPDATE feature_requests SET state='backlog',updated_at=? WHERE id=? AND chat_id=? AND state='active'", now.Unix(), round.WinnerID, round.ChatID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return featurevote.ErrConflict
	}
	return nil
}

func createFeatureRunoff(ctx context.Context, tx *sql.Tx, round featurevote.Round, leaders []featurevote.Option, token string, now time.Time) (int64, error) {
	result, err := tx.ExecContext(ctx, `INSERT INTO feature_rounds(chat_id,slot_at,token,state,closes_at,next_attempt,parent_id)
 VALUES(?,?,?,'planned',?,?,?)`, round.ChatID, now.Unix(), token, now.Add(featurevote.RunoffDuration).Unix(), now.Unix(), round.ID)
	if err != nil {
		return 0, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	for position, option := range leaders {
		if _, err = tx.ExecContext(ctx, `INSERT INTO feature_round_options(round_id,position,idea_id,title,body)
 VALUES(?,?,?,?,?)`, id, position, option.IdeaID, option.Title, option.Text); err != nil {
			return 0, err
		}
	}
	return id, nil
}

func setFeatureRoundReady(ctx context.Context, tx *sql.Tx, round featurevote.Round, from featurevote.RoundState) error {
	result, err := tx.ExecContext(ctx, `UPDATE feature_rounds SET state='ready',winner_id=NULLIF(?,0),runoff_id=NULLIF(?,0),outcome=?,next_attempt=0,error_code=''
 WHERE id=? AND state=?`, round.WinnerID, round.RunoffID, round.Outcome, round.ID, from)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return featurevote.ErrConflict
	}
	return nil
}

func featureOptionsTx(ctx context.Context, tx *sql.Tx, roundID int64) ([]featurevote.Option, error) {
	rows, err := tx.QueryContext(ctx, `SELECT round_id,idea_id,position,votes,title,body
 FROM feature_round_options WHERE round_id=? ORDER BY position`, roundID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var options []featurevote.Option
	for rows.Next() {
		var option featurevote.Option
		if err = rows.Scan(&option.RoundID, &option.IdeaID, &option.Position, &option.Votes, &option.Title, &option.Text); err != nil {
			return nil, err
		}
		options = append(options, option)
	}
	return options, rows.Err()
}

func tallyFeatureOptions(ctx context.Context, tx *sql.Tx, options []featurevote.Option) ([]featurevote.Option, int, error) {
	maxVotes, total := 0, 0
	for i := range options {
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM feature_votes WHERE round_id=? AND idea_id=?", options[i].RoundID, options[i].IdeaID).Scan(&options[i].Votes); err != nil {
			return nil, 0, err
		}
		total += options[i].Votes
		maxVotes = max(maxVotes, options[i].Votes)
	}
	var leaders []featurevote.Option
	if maxVotes > 0 {
		for _, option := range options {
			if option.Votes == maxVotes {
				leaders = append(leaders, option)
			}
		}
	}
	return leaders, total, nil
}

func (s *Store) PublishFeatureRound(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, "UPDATE feature_rounds SET state='published' WHERE id=? AND state='publishing'", id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return featurevote.ErrConflict
	}
	return nil
}

func (s *Store) DeferFeatureRound(ctx context.Context, id int64, from, to featurevote.RoundState, next time.Time, code string) error {
	result, err := s.db.ExecContext(ctx, "UPDATE feature_rounds SET state=?,next_attempt=?,error_code=? WHERE id=? AND state=?", to, next.Unix(), code, id, from)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return featurevote.ErrConflict
	}
	return nil
}

func (s *Store) DisableFeatureSchedule(ctx context.Context, chat int64) error {
	_, err := s.db.ExecContext(ctx, "UPDATE feature_vote_schedules SET enabled=0 WHERE chat_id=?", chat)
	return err
}

func (s *Store) ResolveFeatureRound(ctx context.Context, op, chat, id int64, action featurevote.ResolveAction, now time.Time) error {
	return s.transaction(ctx, &op, func(tx *sql.Tx) error {
		var state featurevote.RoundState
		var outcome string
		if err := tx.QueryRowContext(ctx, "SELECT state,outcome FROM feature_rounds WHERE id=? AND chat_id=?", id, chat).Scan(&state, &outcome); err != nil {
			return err
		}
		if state != featurevote.RoundUnknown && state != featurevote.RoundFailed {
			return featurevote.ErrConflict
		}
		target, targetErr := resolvedFeatureState(action, outcome)
		if targetErr != nil {
			return targetErr
		}
		result, err := tx.ExecContext(ctx, "UPDATE feature_rounds SET state=?,next_attempt=?,error_code='' WHERE id=? AND chat_id=? AND state IN ('unknown','failed')", target, now.Unix(), id, chat)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil || n != 1 {
			return featurevote.ErrConflict
		}
		return nil
	})
}

func resolvedFeatureState(action featurevote.ResolveAction, outcome string) (featurevote.RoundState, error) {
	switch action {
	case featurevote.ResolveRetry:
		if outcome != "" {
			return featurevote.RoundReady, nil
		}
		return featurevote.RoundPlanned, nil
	case featurevote.ResolveSent:
		if outcome != "" {
			return featurevote.RoundPublished, nil
		}
		return featurevote.RoundOpen, nil
	case featurevote.ResolveCancel:
		return featurevote.RoundCancelled, nil
	default:
		return "", featurevote.ErrConflict
	}
}

func (s *Store) RecoverFeatureRounds(ctx context.Context) error {
	return s.transaction(ctx, nil, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "UPDATE feature_rounds SET state='unknown',error_code='restart_during_network' WHERE state IN ('opening','publishing')"); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "UPDATE feature_rounds SET state='open',error_code='' WHERE state='closing'")
		return err
	})
}

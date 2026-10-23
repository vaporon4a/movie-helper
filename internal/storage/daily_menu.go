package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/vaporon4a/movie-helper/internal/daily"
)

func (s *Store) DailyConfig(ctx context.Context, chat int64) (daily.ConfigSnapshot, error) {
	var c daily.ConfigSnapshot
	err := s.transaction(ctx, nil, func(tx *sql.Tx) error {
		if e := tx.QueryRowContext(ctx, "SELECT daily_revision,active,zone FROM chats WHERE chat_id=?", chat).Scan(&c.Revision, &c.Active, &c.Zone); e != nil {
			return e
		}
		rows, e := tx.QueryContext(ctx, "SELECT kind,clock,enabled FROM schedules WHERE chat_id=? ORDER BY kind", chat)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			v := daily.Schedule{ChatID: chat, Zone: c.Zone}
			if e = rows.Scan(&v.Kind, &v.Clock, &v.Enabled); e != nil {
				return e
			}
			c.Schedules = append(c.Schedules, v)
		}
		return rows.Err()
	})
	return c, err
}
func (s *Store) CreateDailyIntent(ctx context.Context, i daily.MenuIntent, now time.Time) error {
	if !daily.ValidKind(i.Kind) || i.PanelID <= 0 {
		return daily.ErrConflict
	}
	return s.transaction(ctx, nil, func(tx *sql.Tx) error {
		var closed bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM daily_menu_panels WHERE chat_id=? AND panel_id=? AND closed=1)", i.ChatID, i.PanelID).Scan(&closed); err != nil {
			return err
		}
		if closed {
			return daily.ErrConflict
		}
		// Expired actor bindings and completed intents are short-lived UI data.
		if _, err := tx.ExecContext(ctx, "DELETE FROM daily_menu_intents WHERE expires_at<=?", now.Unix()); err != nil {
			return err
		}
		if i.Action == "time" {
			if _, err := tx.ExecContext(ctx, "UPDATE daily_menu_intents SET state='cancelled' WHERE chat_id=? AND actor_id=? AND action='time' AND state='pending'", i.ChatID, i.ActorID); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO daily_menu_intents(token,chat_id,panel_id,kind,action,revision,actor_id,expires_at) VALUES(?,?,?,?,?,?,?,?)`, i.Token, i.ChatID, i.PanelID, i.Kind, i.Action, i.Revision, i.ActorID, i.Expires.Unix())
		return err
	})
}

const dailyIntentColumns = "token,chat_id,panel_id,kind,action,revision,actor_id,prompt_id,clock,state,expires_at"

func scanDailyIntent(row interface{ Scan(...any) error }) (daily.MenuIntent, error) {
	var i daily.MenuIntent
	var expires int64
	err := row.Scan(&i.Token, &i.ChatID, &i.PanelID, &i.Kind, &i.Action, &i.Revision, &i.ActorID, &i.PromptID, &i.Clock, &i.State, &expires)
	i.Expires = time.Unix(expires, 0)
	if errors.Is(err, sql.ErrNoRows) {
		err = daily.ErrConflict
	}
	return i, err
}
func (s *Store) DailyIntent(ctx context.Context, token string, chat int64, panel int, now time.Time) (daily.MenuIntent, error) {
	return scanDailyIntent(s.db.QueryRowContext(ctx, "SELECT "+dailyIntentColumns+" FROM daily_menu_intents WHERE token=? AND chat_id=? AND panel_id=? AND state='pending' AND expires_at>? AND NOT EXISTS(SELECT 1 FROM daily_menu_panels p WHERE p.chat_id=daily_menu_intents.chat_id AND p.panel_id=daily_menu_intents.panel_id AND p.closed=1)", token, chat, panel, now.Unix()))
}
func (s *Store) BindDailyPrompt(ctx context.Context, token string, prompt int) error {
	_, err := s.db.ExecContext(ctx, "UPDATE daily_menu_intents SET prompt_id=? WHERE token=? AND state='pending'", prompt, token)
	return err
}
func (s *Store) DailyReplyIntent(ctx context.Context, chat, actor int64, prompt int, now time.Time) (daily.MenuIntent, error) {
	return scanDailyIntent(s.db.QueryRowContext(ctx, "SELECT "+dailyIntentColumns+" FROM daily_menu_intents WHERE chat_id=? AND actor_id=? AND prompt_id=? AND action='time' AND state='pending' AND expires_at>?", chat, actor, prompt, now.Unix()))
}
func (s *Store) CancelDailyIntents(ctx context.Context, chat int64, panel int, now time.Time) ([]int, error) {
	var prompts []int
	err := s.transaction(ctx, nil, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, "SELECT prompt_id FROM daily_menu_intents WHERE chat_id=? AND panel_id=? AND state='pending' AND prompt_id>0", chat, panel)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id int
			if err = rows.Scan(&id); err != nil {
				_ = rows.Close()
				return err
			}
			prompts = append(prompts, id)
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE daily_menu_intents SET state='cancelled',actor_id=0 WHERE chat_id=? AND panel_id=? AND state IN ('pending','sending')", chat, panel)
		return err
	})
	return prompts, err
}
func (s *Store) ApplyDailyIntent(ctx context.Context, i daily.MenuIntent, now time.Time) (bool, error) {
	changed := false
	err := s.transaction(ctx, nil, func(tx *sql.Tx) error {
		current, err := scanDailyIntent(tx.QueryRowContext(ctx, "SELECT "+dailyIntentColumns+" FROM daily_menu_intents WHERE token=? AND chat_id=? AND panel_id=? AND state='pending' AND expires_at>? AND NOT EXISTS(SELECT 1 FROM daily_menu_panels p WHERE p.chat_id=daily_menu_intents.chat_id AND p.panel_id=daily_menu_intents.panel_id AND p.closed=1)", i.Token, i.ChatID, i.PanelID, now.Unix()))
		if err != nil {
			return err
		}
		if current.Action != i.Action || current.ActorID != i.ActorID {
			return daily.ErrConflict
		}
		changed, err = applyDailyConfig(ctx, tx, current, i.Clock, now)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE daily_menu_intents SET state='done',actor_id=0,clock=? WHERE token=?", i.Clock, i.Token)
		return err
	})
	return changed, err
}
func applyDailyConfig(ctx context.Context, tx *sql.Tx, i daily.MenuIntent, clock string, now time.Time) (bool, error) {
	var rev int64
	var active, enabled bool
	var zone, currentClock string
	err := tx.QueryRowContext(ctx, `SELECT c.daily_revision,c.active,c.zone,s.enabled,s.clock FROM chats c JOIN schedules s USING(chat_id) WHERE c.chat_id=? AND s.kind=?`, i.ChatID, i.Kind).Scan(&rev, &active, &zone, &enabled, &currentClock)
	if err != nil {
		return false, err
	}
	if rev != i.Revision {
		return false, daily.ErrConflict
	}
	nextEnabled := enabled
	nextClock := currentClock
	switch i.Action {
	case "enable":
		if !active || zone == "" {
			return false, daily.ErrConflict
		}
		nextEnabled = true
	case "pause":
		nextEnabled = false
	case "time":
		if _, e := time.Parse("15:04", clock); e != nil || len(clock) != 5 {
			return false, daily.ErrConflict
		}
		nextClock = clock
	default:
		return false, daily.ErrConflict
	}
	if nextEnabled == enabled && nextClock == currentClock {
		return false, nil
	}
	if err = cancelPending(ctx, tx, i.ChatID, i.Kind); err != nil {
		return false, err
	}
	_, err = tx.ExecContext(ctx, "UPDATE schedules SET clock=?,enabled=?,effective=? WHERE chat_id=? AND kind=?", nextClock, nextEnabled, now.Unix(), i.ChatID, i.Kind)
	return true, err
}

func (s *Store) CancelDailyActorInputs(ctx context.Context, chat, actor int64) ([]int, error) {
	var prompts []int
	err := s.transaction(ctx, nil, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, "SELECT prompt_id FROM daily_menu_intents WHERE chat_id=? AND actor_id=? AND action='time' AND state='pending' AND prompt_id>0", chat, actor)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id int
			if err = rows.Scan(&id); err != nil {
				_ = rows.Close()
				return err
			}
			prompts = append(prompts, id)
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE daily_menu_intents SET state='cancelled',actor_id=0 WHERE chat_id=? AND actor_id=? AND action='time' AND state='pending'", chat, actor)
		return err
	})
	return prompts, err
}

func (s *Store) CloseDailyPanel(ctx context.Context, chat int64, panel int) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO daily_menu_panels(chat_id,panel_id,closed) VALUES(?,?,1) ON CONFLICT(chat_id,panel_id) DO UPDATE SET closed=1", chat, panel)
	return err
}
func (s *Store) DailyPanelClosed(ctx context.Context, chat int64, panel int) (bool, error) {
	var closed bool
	err := s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM daily_menu_panels WHERE chat_id=? AND panel_id=? AND closed=1)", chat, panel).Scan(&closed)
	return closed, err
}

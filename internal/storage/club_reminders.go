package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/vaporon4a/movie-helper/internal/clubreminder"
	"github.com/vaporon4a/movie-helper/internal/daily"
)

func cancelClub(ctx context.Context, tx *sql.Tx, chat int64, now time.Time) error {
	exists, err := tableExists(ctx, tx, "club_reminder_settings")
	if err != nil || !exists {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE club_reminder_texts SET state='ready' WHERE state='reserved' AND id IN
 (SELECT text_id FROM club_reminder_deliveries WHERE chat_id=? AND state IN ('ready','retry'))`, chat); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "UPDATE club_reminder_deliveries SET state='cancelled' WHERE chat_id=? AND state IN ('ready','retry')", chat)
	if err != nil {
		return err
	}
	return trimClubStock(ctx, tx, now)
}

// Returned reservations must not grow the ready stock beyond its target.
func trimClubStock(ctx context.Context, tx *sql.Tx, now time.Time) error {
	_, err := tx.ExecContext(ctx, `UPDATE club_reminder_texts SET state='expired' WHERE id IN
 (SELECT t.id FROM club_reminder_texts t WHERE t.state='ready' AND
 (SELECT count(*) FROM club_reminder_texts older WHERE older.chat_id=t.chat_id AND older.state='ready' AND older.created_at>=? AND older.id<=t.id)>?)`, now.AddDate(0, 0, -clubreminder.StockDays).Unix(), clubreminder.Target)
	return err
}
func (s *Store) SetClubSchedule(ctx context.Context, op, chat int64, weekday, ordinal int, clock string, now time.Time) error {
	if err := clubreminder.Validate(weekday, ordinal, clock); err != nil {
		return err
	}
	return s.transaction(ctx, &op, func(tx *sql.Tx) error {
		c, err := readClubConfig(ctx, tx, chat)
		if err != nil {
			return err
		}
		_, err = patchClubSettings(ctx, tx, c, clubreminder.ConfigChange{Weekday: new(weekday), Ordinal: new(ordinal), Clock: new(clock), Enabled: new(true)}, now)
		return err
	})
}
func (s *Store) PauseClub(ctx context.Context, op, chat int64, now time.Time) error {
	return s.transaction(ctx, &op, func(tx *sql.Tx) error {
		c, err := readClubConfig(ctx, tx, chat)
		if err != nil {
			return err
		}
		_, err = patchClubSettings(ctx, tx, c, clubreminder.ConfigChange{Enabled: new(false)}, now)
		return err
	})
}

const clubSettingColumns = "s.chat_id,s.weekday,s.ordinal,s.clock,c.zone,s.enabled,s.revision,s.effective_at"

func scanClubSetting(row interface{ Scan(...any) error }) (clubreminder.Settings, error) {
	var s clubreminder.Settings
	err := row.Scan(&s.ChatID, &s.Weekday, &s.Ordinal, &s.Clock, &s.Zone, &s.Enabled, &s.Revision, &s.Effective)
	return s, err
}
func (s *Store) ClubSettings(ctx context.Context, chat int64, now time.Time) (clubreminder.Settings, error) {
	settings, err := scanClubSetting(s.db.QueryRowContext(ctx, "SELECT "+clubSettingColumns+" FROM club_reminder_settings s JOIN chats c USING(chat_id) WHERE s.chat_id=?", chat))
	if errors.Is(err, sql.ErrNoRows) {
		return clubreminder.Settings{ChatID: chat}, nil
	}
	if err != nil {
		return settings, err
	}
	err = s.db.QueryRowContext(ctx, "SELECT count(*) FROM club_reminder_texts WHERE chat_id=? AND state='ready' AND created_at>=?", chat, now.AddDate(0, 0, -clubreminder.StockDays).Unix()).Scan(&settings.Stock)
	if err != nil {
		return settings, err
	}
	if e := s.db.QueryRowContext(ctx, "SELECT last_error_code,next_attempt FROM ai_work WHERE work_key=?", fmt.Sprintf("club:%d", chat)).Scan(&settings.LastAIError, &settings.NextRefill); e != nil && !errors.Is(e, sql.ErrNoRows) {
		return settings, e
	}
	settings.Issues, err = s.clubDeliveries(ctx, "chat_id=? AND state IN ('unknown','failed') ORDER BY id DESC LIMIT 5", chat)
	return settings, err
}
func (s *Store) ClubSchedules(ctx context.Context) ([]clubreminder.Settings, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+clubSettingColumns+" FROM club_reminder_settings s JOIN chats c USING(chat_id) WHERE s.enabled=1 AND c.active=1")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []clubreminder.Settings
	for rows.Next() {
		value, e := scanClubSetting(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, value)
	}
	return out, rows.Err()
}
func (s *Store) ReserveClub(ctx context.Context, settings clubreminder.Settings, slot clubreminder.Slot) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO club_reminder_deliveries(chat_id,revision,cycle_date,intent,slot_at,deadline,state)
 SELECT s.chat_id,s.revision,?,?,?,?,'ready' FROM club_reminder_settings s JOIN chats c USING(chat_id)
 WHERE s.chat_id=? AND s.enabled=1 AND c.active=1 AND s.revision=? AND c.zone=? AND s.effective_at<?
 AND NOT EXISTS(SELECT 1 FROM club_reminder_deliveries d WHERE d.chat_id=s.chat_id AND d.cycle_date=? AND d.intent=? AND d.state IN ('ready','retry','sending','sent','unknown'))`, slot.Cycle, slot.Intent, slot.At.Unix(), slot.At.Add(clubreminder.DeliveryWindow).Unix(), settings.ChatID, settings.Revision, settings.Zone, slot.At.Unix(), slot.Cycle, slot.Intent)
	return err
}

const clubDeliveryColumns = "id,chat_id,revision,deadline,slot_at,next_attempt,intent,state,error_code,COALESCE(text_id,0),text_snapshot,tone,provider"

func scanClubDelivery(row interface{ Scan(...any) error }) (clubreminder.Delivery, error) {
	var d clubreminder.Delivery
	err := row.Scan(&d.ID, &d.ChatID, &d.Revision, &d.Deadline, &d.SlotAt, &d.NextAttempt, &d.Intent, &d.State, &d.Error, &d.Text.ID, &d.Text.Body, &d.Text.Tone, &d.Text.Provider)
	d.Text.Intent = d.Intent
	return d, err
}
func (s *Store) clubDeliveries(ctx context.Context, where string, args ...any) ([]clubreminder.Delivery, error) {
	// #nosec G202 -- private fixed predicates, all user values are parameter-bound.
	rows, err := s.db.QueryContext(ctx, "SELECT "+clubDeliveryColumns+" FROM club_reminder_deliveries WHERE "+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []clubreminder.Delivery
	for rows.Next() {
		d, e := scanClubDelivery(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
func (s *Store) ClubDue(ctx context.Context, now time.Time) ([]clubreminder.Delivery, error) {
	if err := s.expireClub(ctx, now); err != nil {
		return nil, err
	}
	return s.clubDeliveries(ctx, "state IN ('ready','retry') AND next_attempt<=? AND slot_at<=? AND deadline>? ORDER BY id", now.Unix(), now.Unix(), now.Unix())
}
func (s *Store) expireClub(ctx context.Context, now time.Time) error {
	return s.transaction(ctx, nil, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE club_reminder_texts SET state='expired' WHERE state='ready' AND created_at<?`, now.AddDate(0, 0, -clubreminder.StockDays).Unix()); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE club_reminder_texts SET state='ready' WHERE state='reserved' AND id IN (SELECT text_id FROM club_reminder_deliveries WHERE state IN ('ready','retry') AND deadline<=?)`, now.Unix()); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "UPDATE club_reminder_deliveries SET state='expired' WHERE state IN ('ready','retry') AND deadline<=?", now.Unix())
		if err != nil {
			return err
		}
		return trimClubStock(ctx, tx, now)
	})
}
func (s *Store) ClaimClub(ctx context.Context, id int64, now time.Time, fallback clubreminder.Text) (clubreminder.Delivery, bool, error) {
	var d clubreminder.Delivery
	claimed := false
	err := s.transaction(ctx, nil, func(tx *sql.Tx) error {
		value, e := scanClubDelivery(tx.QueryRowContext(ctx, "SELECT "+clubDeliveryColumns+` FROM club_reminder_deliveries WHERE id=? AND state IN ('ready','retry') AND next_attempt<=? AND deadline>? AND slot_at<=?
 AND EXISTS(SELECT 1 FROM club_reminder_settings s JOIN chats c USING(chat_id) WHERE s.chat_id=club_reminder_deliveries.chat_id AND s.enabled=1 AND c.active=1 AND s.revision=club_reminder_deliveries.revision)`, id, now.Unix(), now.Unix(), now.Unix()))
		if errors.Is(e, sql.ErrNoRows) {
			return nil
		}
		if e != nil {
			return e
		}
		d = value
		if d.Text.Body == "" {
			if e = attachClubText(ctx, tx, &d, now, fallback); e != nil {
				return e
			}
		}
		result, e := tx.ExecContext(ctx, "UPDATE club_reminder_deliveries SET state='sending',text_id=?,text_snapshot=?,tone=?,provider=? WHERE id=? AND state IN ('ready','retry')", nullableClubID(d.Text.ID), d.Text.Body, d.Text.Tone, d.Text.Provider, id)
		if e != nil {
			return e
		}
		count, e := result.RowsAffected()
		claimed = count == 1
		return e
	})
	return d, claimed, err
}
func nullableClubID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}
func attachClubText(ctx context.Context, tx *sql.Tx, d *clubreminder.Delivery, now time.Time, fallback clubreminder.Text) error {
	err := tx.QueryRowContext(ctx, "SELECT id,text,tone,provider FROM club_reminder_texts WHERE chat_id=? AND intent=? AND state='ready' AND created_at>=? ORDER BY id LIMIT 1", d.ChatID, d.Intent, now.AddDate(0, 0, -clubreminder.StockDays).Unix()).Scan(&d.Text.ID, &d.Text.Body, &d.Text.Tone, &d.Text.Provider)
	if errors.Is(err, sql.ErrNoRows) {
		if !clubreminder.ValidText(fallback) || fallback.Intent != d.Intent {
			return errors.New("invalid reminder fallback")
		}
		d.Text = fallback
		return nil
	}
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "UPDATE club_reminder_texts SET state='reserved' WHERE id=? AND state='ready'", d.Text.ID)
	return err
}
func (s *Store) FinishClub(ctx context.Context, d clubreminder.Delivery, state string, message int, next time.Time, code string, now time.Time) error {
	return s.transaction(ctx, nil, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, "UPDATE club_reminder_deliveries SET state=?,message_id=?,next_attempt=?,error_code=? WHERE id=? AND state='sending'", state, message, next.Unix(), code, d.ID)
		if err = changedClubDelivery(result, err); err != nil {
			return err
		}
		if state == clubreminder.Sent && d.Text.ID != 0 {
			if _, err = tx.ExecContext(ctx, "UPDATE club_reminder_texts SET state='used' WHERE id=?", d.Text.ID); err != nil {
				return err
			}
		}
		if code == "telegram_forbidden" {
			if err = cancelClub(ctx, tx, d.ChatID, now); err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, "UPDATE club_reminder_settings SET enabled=0 WHERE chat_id=?", d.ChatID)
		}
		return err
	})
}
func (s *Store) RecoverClub(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, "UPDATE club_reminder_deliveries SET state='unknown',error_code='restart_during_network' WHERE state='sending'")
	return err
}
func (s *Store) ResolveClub(ctx context.Context, op, chat, id int64, action string, now time.Time) error {
	if action != sentState && action != retryState && action != "cancel" {
		return daily.ErrConflict
	}
	return s.transaction(ctx, &op, func(tx *sql.Tx) error {
		return resolveClub(ctx, tx, chat, id, action, now)
	})
}

func updateClubZone(ctx context.Context, tx *sql.Tx, chat int64, now time.Time) error {
	exists, err := tableExists(ctx, tx, "club_reminder_settings")
	if err != nil || !exists {
		return err
	}
	if err = cancelClub(ctx, tx, chat, now); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "UPDATE club_reminder_settings SET revision=revision+1,effective_at=? WHERE chat_id=?", now.Unix(), chat)
	return err
}
func suspendClub(ctx context.Context, tx *sql.Tx, chat int64, now time.Time) error {
	exists, err := tableExists(ctx, tx, "club_reminder_settings")
	if err != nil || !exists {
		return err
	}
	if err = cancelClub(ctx, tx, chat, now); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "UPDATE club_reminder_settings SET enabled=0 WHERE chat_id=?", chat)
	return err
}

func clubResolveTarget(action string, d clubreminder.Delivery, now time.Time) (string, error) {
	switch action {
	case sentState:
		return clubreminder.Sent, nil
	case retryState:
		if d.Deadline <= now.Unix() {
			return "", errors.New("reminder expired")
		}
		return clubreminder.Retry, nil
	default:
		return cancelledState, nil
	}
}

func (s *Store) ClubFallbackCount(ctx context.Context, chat int64, intent string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM club_reminder_deliveries WHERE chat_id=? AND intent=? AND text_snapshot!=''", chat, intent).Scan(&count)
	return count, err
}

func changedClubDelivery(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return daily.ErrConflict
	}
	return nil
}

func resolveClub(ctx context.Context, tx *sql.Tx, chat, id int64, action string, now time.Time) error {

	d, err := scanClubDelivery(tx.QueryRowContext(ctx, "SELECT "+clubDeliveryColumns+" FROM club_reminder_deliveries WHERE id=? AND chat_id=? AND state='unknown'", id, chat))
	if err != nil {
		return err
	}
	target, err := clubResolveTarget(action, d, now)
	if err != nil {
		return err
	}

	result, err := tx.ExecContext(ctx, "UPDATE club_reminder_deliveries SET state=?,next_attempt=?,error_code='' WHERE id=? AND state='unknown'", target, now.Unix(), id)
	if err = changedClubDelivery(result, err); err != nil {
		return err
	}
	if target == clubreminder.Sent || target == cancelledState {
		_, err = tx.ExecContext(ctx, "UPDATE club_reminder_texts SET state='used' WHERE id=?", d.Text.ID)
	}
	return err
}

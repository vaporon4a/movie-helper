package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/vaporon4a/movie-helper/internal/aiwork"
	"github.com/vaporon4a/movie-helper/internal/clubreminder"
)

func (s *Store) ClubTexts(ctx context.Context, chat int64, now time.Time) ([]clubreminder.Text, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,text,intent,tone,provider,state FROM club_reminder_texts WHERE chat_id=? AND
 ((state='ready' AND created_at>=?) OR state='reserved' OR (state='used' AND id IN (SELECT id FROM club_reminder_texts WHERE chat_id=? AND state='used' ORDER BY id DESC LIMIT 10))) ORDER BY id`, chat, now.AddDate(0, 0, -clubreminder.StockDays).Unix(), chat)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var texts []clubreminder.Text
	for rows.Next() {
		var text clubreminder.Text
		if err = rows.Scan(&text.ID, &text.Body, &text.Intent, &text.Tone, &text.Provider, &text.State); err != nil {
			return nil, err
		}
		texts = append(texts, text)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	fallbackRows, err := s.db.QueryContext(ctx, "SELECT text_snapshot,intent,tone FROM club_reminder_deliveries WHERE chat_id=? AND state='sent' AND text_id IS NULL ORDER BY id DESC LIMIT 10", chat)
	if err != nil {
		return nil, err
	}
	defer fallbackRows.Close()
	for fallbackRows.Next() {
		var text clubreminder.Text
		text.State = "used"
		text.Provider = "template"
		if err = fallbackRows.Scan(&text.Body, &text.Intent, &text.Tone); err != nil {
			return nil, err
		}
		texts = append(texts, text)
	}
	return texts, fallbackRows.Err()
}
func (s *Store) SaveClubText(ctx context.Context, chat int64, text clubreminder.Text, now time.Time) (bool, error) {
	if !clubreminder.ValidText(text) {
		return false, nil
	}
	history, err := s.ClubTexts(ctx, chat, now)
	if err != nil {
		return false, err
	}
	for _, old := range history {
		if clubreminder.Similar(old.Body, text.Body) {
			return false, nil
		}
	}
	inserted := false
	err = s.transaction(ctx, nil, func(tx *sql.Tx) error {
		result, e := tx.ExecContext(ctx, `INSERT OR IGNORE INTO club_reminder_texts(chat_id,intent,tone,text,text_hash,provider,policy_version,created_at,state)
 SELECT ?,?,?,?,?,?,?,?,'ready' WHERE (SELECT count(*) FROM club_reminder_texts WHERE chat_id=? AND state='ready' AND created_at>=?)<?
 AND EXISTS(SELECT 1 FROM club_reminder_settings s JOIN chats c USING(chat_id) WHERE s.chat_id=? AND s.enabled=1 AND c.active=1)`, chat, text.Intent, text.Tone, text.Body, clubreminder.Hash(text.Body), text.Provider, clubreminder.Policy, now.Unix(), chat, now.AddDate(0, 0, -clubreminder.StockDays).Unix(), clubreminder.Target, chat)
		if e != nil {
			return e
		}
		count, e := result.RowsAffected()
		inserted = count == 1
		return e
	})
	return inserted, err
}
func seedClubWork(ctx context.Context, tx *sql.Tx, now time.Time) error {
	rows, err := tx.QueryContext(ctx, `SELECT s.chat_id FROM club_reminder_settings s JOIN chats c USING(chat_id) WHERE s.enabled=1 AND c.active=1
 AND (SELECT count(*) FROM club_reminder_texts t WHERE t.chat_id=s.chat_id AND t.state='ready' AND t.created_at>=?)<?`, now.AddDate(0, 0, -clubreminder.StockDays).Unix(), clubreminder.Target)
	if err != nil {
		return err
	}
	defer rows.Close()
	var chats []int64
	for rows.Next() {
		var chat int64
		if err = rows.Scan(&chat); err != nil {
			return err
		}
		chats = append(chats, chat)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	for _, chat := range chats {
		if err = enqueueAIWorkTx(ctx, tx, aiwork.ClubReminderRefill, chat, fmt.Sprintf("club:%d", chat), aiwork.PriorityBackground, now); err != nil {
			return err
		}
	}
	return nil
}

package storage

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/pressly/goose/v3"
	"github.com/vaporon4a/movie-helper/internal/daily"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Store struct{ db *sql.DB }

func Open(ctx context.Context, path string) (*Store, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(abs), 0700); err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: abs}
	db, err := sql.Open("sqlite", u.String()+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	p, err := s.migrator()
	if err == nil {
		_, err = p.Up(ctx)
	}
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}
func (s *Store) migrator() (*goose.Provider, error) {
	f, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return nil, err
	}
	return goose.NewProvider(goose.DialectSQLite3, s.db, f)
}
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) transaction(ctx context.Context, operation *int64, f func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if operation != nil {
		r, e := tx.ExecContext(ctx, "INSERT OR IGNORE INTO operations(update_id) VALUES(?)", *operation)
		if e != nil {
			return e
		}
		n, e := r.RowsAffected()
		if e != nil {
			return e
		}
		if n == 0 {
			return daily.ErrDuplicate
		}
	}
	if err = f(tx); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) EnsureChat(ctx context.Context, chat int64) error {
	return s.transaction(ctx, nil, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO chats(chat_id) VALUES(?)", chat); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO schedules(chat_id,kind,clock) VALUES(?,'meme','09:00'),(?,'fact','12:00')", chat, chat)
		return err
	})
}
func cancelPending(ctx context.Context, tx *sql.Tx, chat int64, kind string) error {
	if _, err := tx.ExecContext(ctx, `UPDATE items SET state='approved' WHERE state='reserved' AND id IN
 (SELECT item_id FROM deliveries WHERE chat_id=? AND (?='' OR kind=?) AND state IN ('preparing','ready','retry'))`, chat, kind, kind); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, "UPDATE deliveries SET state='cancelled' WHERE chat_id=? AND (?='' OR kind=?) AND state IN ('preparing','ready','retry')", chat, kind, kind)
	return err
}
func (s *Store) SetZone(ctx context.Context, op, chat int64, zone string, now time.Time) error {
	if _, err := time.LoadLocation(zone); err != nil || zone == "" || zone == "Local" {
		return errors.New("invalid timezone")
	}
	return s.transaction(ctx, &op, func(tx *sql.Tx) error {
		if err := cancelPending(ctx, tx, chat, ""); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE chats SET zone=? WHERE chat_id=?", zone, chat); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE schedules SET effective=? WHERE chat_id=?", now.Unix(), chat); err != nil {
			return err
		}
		exists, err := tableExists(ctx, tx, "movie_poll_schedules")
		if err != nil || !exists {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE movie_rounds SET state='cancelled',error_code='timezone_changed' WHERE chat_id=? AND state='planned'", chat); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE movie_poll_schedules SET effective=? WHERE chat_id=?", now.Unix(), chat)
		return err
	})
}
func (s *Store) SetSchedule(ctx context.Context, op, chat int64, kind, clock string, enabled bool, now time.Time) error {
	if !daily.ValidKind(kind) {
		return errors.New("invalid kind")
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
		// Changing settings cancels waiting tasks. A request already in flight may finish.
		if err := cancelPending(ctx, tx, chat, kind); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "UPDATE schedules SET clock=CASE WHEN ? THEN ? ELSE clock END,enabled=?,effective=? WHERE chat_id=? AND kind=?", enabled, clock, enabled, now.Unix(), chat, kind)
		return err
	})
}
func (s *Store) Suspend(ctx context.Context, chat int64) error {
	return s.transaction(ctx, nil, func(tx *sql.Tx) error {
		if err := cancelPending(ctx, tx, chat, ""); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE schedules SET enabled=0 WHERE chat_id=?", chat); err != nil {
			return err
		}
		exists, err := tableExists(ctx, tx, "movie_poll_schedules")
		if err != nil {
			return err
		}
		if exists {
			if _, err = tx.ExecContext(ctx, "UPDATE movie_poll_schedules SET enabled=0 WHERE chat_id=?", chat); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, "UPDATE chats SET active=0 WHERE chat_id=?", chat)
		return err
	})
}

func tableExists(ctx context.Context, tx *sql.Tx, name string) (bool, error) {
	var exists bool
	err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name=?)", name).Scan(&exists)
	return exists, err
}
func (s *Store) Resume(ctx context.Context, op, chat int64) error {
	return s.transaction(ctx, &op, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "UPDATE chats SET active=1 WHERE chat_id=?", chat)
		return err
	})
}
func (s *Store) SetModeration(ctx context.Context, op, chat int64, enabled bool, now time.Time) error {
	return s.transaction(ctx, &op, func(tx *sql.Tx) error {
		var current bool
		if err := tx.QueryRowContext(ctx, "SELECT moderation FROM chats WHERE chat_id=?", chat).Scan(&current); err != nil {
			return err
		}
		if current == enabled {
			return nil
		}
		if err := cancelPending(ctx, tx, chat, ""); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE chats SET moderation=? WHERE chat_id=?", enabled, chat); err != nil {
			return err
		}
		// AI approval survives mode changes, but cannot substitute for a human
		// approval while moderation is enabled. Human submissions stay untouched.
		from, to := "pending", "approved"
		if enabled {
			from, to = "approved", "pending"
		}
		if _, err := tx.ExecContext(ctx, "UPDATE items SET state=? WHERE chat_id=? AND ai_approved=1 AND approved_by IS NULL AND state=?", to, chat, from); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "UPDATE schedules SET effective=? WHERE chat_id=?", now.Unix(), chat)
		return err
	})
}
func (s *Store) Schedules(ctx context.Context, chat int64) ([]daily.Schedule, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT s.chat_id,s.kind,s.clock,c.zone,s.enabled AND c.active,s.effective,c.moderation
 FROM schedules s JOIN chats c USING(chat_id) WHERE (?=0 OR s.chat_id=?) ORDER BY s.chat_id,s.kind`, chat, chat)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []daily.Schedule
	for rows.Next() {
		var x daily.Schedule
		if err = rows.Scan(&x.ChatID, &x.Kind, &x.Clock, &x.Zone, &x.Enabled, &x.Effective, &x.Moderation); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
func (s *Store) Add(ctx context.Context, op int64, i daily.Item, now time.Time) (int64, error) {
	if err := i.Validate(); err != nil {
		return 0, err
	}
	var id int64
	err := s.transaction(ctx, &op, func(tx *sql.Tx) error {
		r, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO items(chat_id,kind,text,source,image,content_key,author_id,state,created_at)
  VALUES(?,?,?,?,?,?,?,'pending',?)`, i.ChatID, i.Kind, i.Text, i.Source, i.Image, i.Key, i.AuthorID, now.Unix())
		if err != nil {
			return err
		}
		n, _ := r.RowsAffected()
		if n == 0 {
			return daily.ErrDuplicate
		}
		id, err = r.LastInsertId()
		return err
	})
	return id, err
}

const itemColumns = "id,chat_id,kind,text,source,image,content_key,author_id,state"

func scanItem(row interface{ Scan(...any) error }) (daily.Item, error) {
	var x daily.Item
	err := row.Scan(&x.ID, &x.ChatID, &x.Kind, &x.Text, &x.Source, &x.Image, &x.Key, &x.AuthorID, &x.State)
	return x, err
}
func (s *Store) Queue(ctx context.Context, chat int64, after int64) ([]daily.Item, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+itemColumns+" FROM items WHERE chat_id=? AND id>? AND state IN ('pending','approved') ORDER BY id LIMIT 10", chat, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []daily.Item
	for rows.Next() {
		x, e := scanItem(rows)
		if e != nil {
			return nil, e
		}
		items = append(items, x)
	}
	return items, rows.Err()
}
func (s *Store) Item(ctx context.Context, chat, id int64) (daily.Item, error) {
	return scanItem(s.db.QueryRowContext(ctx, "SELECT "+itemColumns+" FROM items WHERE chat_id=? AND id=?", chat, id))
}
func (s *Store) Moderate(ctx context.Context, op, chat, id, user int64, approve bool, now time.Time) error {
	return s.transaction(ctx, &op, func(tx *sql.Tx) error {
		target := "rejected"
		from := "state IN ('pending','approved')"
		if approve {
			target = "approved"
			from = "(state='pending' OR (state='approved' AND approved_by IS NULL))"
		}
		r, err := tx.ExecContext(ctx, "UPDATE items SET state=?,approved_by=?,approved_at=? WHERE id=? AND chat_id=? AND "+from, target, user, now.Unix(), id, chat)
		if err != nil {
			return err
		}
		n, _ := r.RowsAffected()
		if n == 0 {
			return daily.ErrConflict
		}
		return nil
	})
}
func (s *Store) Seen(ctx context.Context, chat int64, kind, key string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM items WHERE chat_id=? AND kind=? AND content_key=?", chat, kind, key).Scan(&n)
	return n > 0, err
}

// Recover is only called at startup while the process holds an exclusive DB lock.
func (s *Store) Recover(ctx context.Context) error {
	return s.transaction(ctx, nil, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "UPDATE deliveries SET state='unknown' WHERE state='sending'"); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "UPDATE deliveries SET fetch_claimed=0,next_attempt=MAX(next_attempt,strftime('%s','now')+300) WHERE state='preparing' AND fetch_claimed=1")
		return err
	})
}

// Reserve freezes a local-day slot before fetching from an external provider.
func (s *Store) Reserve(ctx context.Context, sc daily.Schedule, date string, slot, deadline int64) (int64, error) {
	var id int64
	err := s.transaction(ctx, nil, func(tx *sql.Tx) error {
		r, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO deliveries(chat_id,kind,local_date,slot_at,deadline,state)
  SELECT s.chat_id,s.kind,?,?,?,'preparing' FROM schedules s JOIN chats c USING(chat_id)
  WHERE s.chat_id=? AND s.kind=? AND s.enabled=1 AND c.active=1 AND s.effective=? AND c.zone=? AND s.clock=? AND c.moderation=?`, date, slot, deadline, sc.ChatID, sc.Kind, sc.Effective, sc.Zone, sc.Clock, sc.Moderation)
		if err != nil {
			return err
		}
		n, _ := r.RowsAffected()
		if n > 0 {
			id, err = r.LastInsertId()
		}
		return err
	})
	return id, err
}
func (s *Store) Attach(ctx context.Context, id int64, candidate *daily.Item, now time.Time) error {
	return s.transaction(ctx, nil, func(tx *sql.Tx) error {
		var chat int64
		var kind string
		var moderation bool
		if err := tx.QueryRowContext(ctx, "SELECT d.chat_id,d.kind,c.moderation FROM deliveries d JOIN chats c USING(chat_id) WHERE d.id=? AND d.state='preparing' AND d.deadline>?", id, now.Unix()).Scan(&chat, &kind, &moderation); err != nil {
			return err
		}
		var item int64
		err := tx.QueryRowContext(ctx, "SELECT id FROM items WHERE chat_id=? AND kind=? AND state='approved' AND ((? AND approved_by IS NOT NULL) OR (NOT ? AND ai_approved=1)) ORDER BY id LIMIT 1", chat, kind, moderation, moderation).Scan(&item)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if errors.Is(err, sql.ErrNoRows) && candidate != nil {
			i := *candidate
			i.ChatID = chat
			i.Kind = kind
			if err = i.Validate(); err != nil {
				return err
			}
			state := "approved"
			if moderation {
				state = "pending"
			}
			r, e := tx.ExecContext(ctx, `INSERT OR IGNORE INTO items(chat_id,kind,text,source,image,content_key,author_id,state,created_at,ai_approved)
   VALUES(?,?,?,?,?,?,0,?,?,1)`, chat, kind, i.Text, i.Source, i.Image, i.Key, state, now.Unix())
			if e != nil {
				return e
			}
			n, _ := r.RowsAffected()
			if n > 0 && !moderation {
				item, e = r.LastInsertId()
				if e != nil {
					return e
				}
			}
		}
		if item == 0 {
			_, err = tx.ExecContext(ctx, "UPDATE deliveries SET state='skipped' WHERE id=?", id)
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE items SET state='reserved' WHERE id=?", item); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE deliveries SET state='ready',item_id=?,next_attempt=0,fetch_claimed=0 WHERE id=?", item, id)
		return err
	})
}
func (s *Store) HasApproved(ctx context.Context, chat int64, kind string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM items i JOIN chats c USING(chat_id)
 WHERE i.chat_id=? AND i.kind=? AND i.state='approved'
 AND ((c.moderation=1 AND i.approved_by IS NOT NULL) OR (c.moderation=0 AND i.ai_approved=1))`, chat, kind).Scan(&n)
	return n > 0, err
}
func (s *Store) Pending(ctx context.Context) ([]daily.Delivery, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,chat_id,kind,local_date,deadline,next_attempt,state,COALESCE(item_id,0)
 FROM deliveries WHERE state IN ('ready','retry') ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []daily.Delivery
	for rows.Next() {
		var d daily.Delivery
		if err = rows.Scan(&d.ID, &d.ChatID, &d.Kind, &d.Date, &d.Deadline, &d.NextAttempt, &d.State, &d.Item.ID); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	for n := range out {
		out[n].Item, err = s.Item(ctx, out[n].ChatID, out[n].Item.ID)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
func (s *Store) Claim(ctx context.Context, id int64, now time.Time) (bool, error) {
	r, err := s.db.ExecContext(ctx, `UPDATE deliveries SET state='sending' WHERE id=? AND state IN ('ready','retry') AND next_attempt<=? AND deadline>?
 AND EXISTS (SELECT 1 FROM schedules s JOIN chats c USING(chat_id) WHERE s.chat_id=deliveries.chat_id AND s.kind=deliveries.kind AND s.enabled=1 AND c.active=1 AND s.effective < deliveries.slot_at)`, id, now.Unix(), now.Unix())
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	return n == 1, err
}
func (s *Store) Finish(ctx context.Context, id int64, state string, message int, next int64) error {
	switch state {
	case "sent", "unknown", "failed", "retry", "cancelled":
	default:
		return errors.New("invalid delivery state")
	}
	return s.transaction(ctx, nil, func(tx *sql.Tx) error {
		var item int64
		var current string
		if err := tx.QueryRowContext(ctx, "SELECT COALESCE(item_id,0),state FROM deliveries WHERE id=?", id).Scan(&item, &current); err != nil {
			return err
		}
		if current != "sending" && current != "ready" && current != "retry" {
			return daily.ErrConflict
		}
		if _, err := tx.ExecContext(ctx, "UPDATE deliveries SET state=?,message_id=?,next_attempt=? WHERE id=?", state, message, next, id); err != nil {
			return err
		}
		itemState := "reserved"
		switch state {
		case "sent":
			itemState = "sent"
		case "failed":
			itemState = "failed"
		case "cancelled":
			itemState = "approved"
		}
		_, err := tx.ExecContext(ctx, "UPDATE items SET state=? WHERE id=? AND state='reserved'", itemState, item)
		return err
	})
}
func (s *Store) Issues(ctx context.Context, chat int64) ([]daily.Delivery, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,kind,local_date,state,next_attempt,fetch_attempts FROM deliveries WHERE chat_id=? AND state IN ('unknown','failed','preparing','skipped') ORDER BY id DESC LIMIT 10`, chat)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []daily.Delivery
	for rows.Next() {
		var d daily.Delivery
		d.ChatID = chat
		if err = rows.Scan(&d.ID, &d.Kind, &d.Date, &d.State, &d.NextAttempt, &d.FetchAttempts); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
func (s *Store) Resolve(ctx context.Context, op, chat, id int64, sent bool) error {
	return s.transaction(ctx, &op, func(tx *sql.Tx) error {
		var item int64
		if err := tx.QueryRowContext(ctx, "SELECT item_id FROM deliveries WHERE id=? AND chat_id=? AND state='unknown'", id, chat).Scan(&item); err != nil {
			return daily.ErrConflict
		}
		ds, is := "cancelled", "approved"
		if sent {
			ds, is = "sent", "sent"
		}
		if _, err := tx.ExecContext(ctx, "UPDATE deliveries SET state=? WHERE id=?", ds, id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "UPDATE items SET state=? WHERE id=?", is, item)
		return err
	})
}

// AllowAPI reserves an attempt before the request. Restarts and ambiguous HTTP
// failures cannot reset the daily request cap. This is not a monetary budget.
func (s *Store) AllowAPI(ctx context.Context, date string, limit int) (bool, error) {
	if limit <= 0 {
		return false, nil
	}
	r, err := s.db.ExecContext(ctx, `INSERT INTO api_usage(utc_date,requests) VALUES(?,1)
 ON CONFLICT(utc_date) DO UPDATE SET requests=requests+1 WHERE requests<?`, date, limit)
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	return n == 1, err
}

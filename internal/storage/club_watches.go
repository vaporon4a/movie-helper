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

func scanProfileFilm(row interface{ Scan(...any) error }) (clubprofile.Film, error) {
	var f clubprofile.Film
	var raw string
	var fetched int64
	err := row.Scan(&f.TMDBID, &f.Title, &f.OriginalTitle, &f.Year, &raw, &f.Source, &fetched)
	if errors.Is(err, sql.ErrNoRows) {
		return f, clubprofile.ErrNotFound
	}
	if err != nil {
		return f, err
	}
	f.FetchedAt = time.Unix(fetched, 0)
	err = json.Unmarshal([]byte(raw), &f.Metadata)
	return f, err
}

func (s *Store) CachedProfileFilm(ctx context.Context, id int64) (clubprofile.Film, error) {
	return scanProfileFilm(s.db.QueryRowContext(ctx, `SELECT tmdb_id,title,original_title,COALESCE(release_year,0),metadata_json,source,fetched_at FROM club_films WHERE tmdb_id=?`, id))
}

func saveProfileWatch(ctx context.Context, tx *sql.Tx, d clubprofile.WatchDraft, f clubprofile.Film, now time.Time) (int64, error) {
	meta, err := json.Marshal(f.Metadata)
	if err != nil {
		return 0, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO club_films(tmdb_id,title,original_title,release_year,metadata_json,source,fetched_at) VALUES(?,?,?,NULLIF(?,0),?,?,?)
 ON CONFLICT(tmdb_id) DO UPDATE SET title=excluded.title,original_title=excluded.original_title,release_year=excluded.release_year,metadata_json=CASE WHEN json_extract(club_films.metadata_json,'$.complete')=1 AND json_extract(excluded.metadata_json,'$.complete')=0 THEN club_films.metadata_json ELSE excluded.metadata_json END,source=excluded.source,fetched_at=excluded.fetched_at WHERE excluded.fetched_at>=club_films.fetched_at`, f.TMDBID, f.Title, f.OriginalTitle, f.Year, meta, f.Source, f.FetchedAt.Unix())
	if err != nil {
		return 0, err
	}
	if !f.Metadata.Complete {
		if _, err = tx.ExecContext(ctx, "INSERT INTO club_film_enrichment(tmdb_id,next_attempt) VALUES(?,?) ON CONFLICT(tmdb_id) DO NOTHING", f.TMDBID, now.Unix()); err != nil {
			return 0, err
		}
	}
	var existing int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM club_watches WHERE chat_id=? AND tmdb_id=? AND watched_date=? AND date_precision='day' AND state='active' AND ?='day'`, d.ChatID, f.TMDBID, d.Input.Date, d.Input.Precision).Scan(&existing)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	fresh := clubprofile.FreshForReview(d.Input, now)
	mode := "retrospective"
	if fresh {
		mode = "fresh"
	}
	r, err := tx.ExecContext(ctx, `INSERT INTO club_watches(chat_id,tmdb_id,watched_date,date_precision,zone,mode,origin,created_at) VALUES(?,?,NULLIF(?,''),?,?,?,?,?)`, d.ChatID, f.TMDBID, d.Input.Date, d.Input.Precision, d.Input.Zone, mode, fmt.Sprintf("draft:%d", d.ID), now.Unix())
	if err != nil {
		return 0, err
	}
	id, err := r.LastInsertId()
	if err != nil {
		return 0, err
	}
	w := clubprofile.WatchEvent{ID: id, ChatID: d.ChatID, TMDBID: f.TMDBID, Date: d.Input.Date, DatePrecision: d.Input.Precision, Zone: d.Input.Zone, Mode: mode, State: "active", Revision: 1, Film: f}
	if err = recordWatchAudit(ctx, tx, w, "added", now); err != nil {
		return 0, err
	}
	if fresh {
		err = createReviewSession(ctx, tx, w, now)
	}
	return id, err
}

func createReviewSession(ctx context.Context, tx *sql.Tx, w clubprofile.WatchEvent, now time.Time) error {
	var enabled bool
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE((SELECT auto_review FROM club_profile_settings WHERE chat_id=?),1)", w.ChatID).Scan(&enabled); err != nil {
		return err
	}
	if !enabled {
		return nil
	}
	plan, err := json.Marshal(clubprofile.DefaultReviewPlan())
	if err != nil {
		return err
	}
	// start_before is retained only for compatibility with the existing DB schema.
	// The sole active clock is finish_before, set when Telegram confirms a send.
	_, err = tx.ExecContext(ctx, `INSERT INTO club_review_sessions(chat_id,watch_id,plan_json,state,start_before,finish_before,created_at) VALUES(?,?,?,'queued',0,0,?)`, w.ChatID, w.ID, plan, now.Unix())
	if err != nil {
		return err
	}
	return enrollResearch(ctx, tx, w, now)
}

const watchColumns = `w.id,w.chat_id,w.tmdb_id,COALESCE(w.watched_date,''),w.date_precision,w.zone,w.mode,w.state,w.revision,f.title,f.original_title,COALESCE(f.release_year,0),f.metadata_json,f.source,f.fetched_at`

func scanProfileWatch(row interface{ Scan(...any) error }) (clubprofile.WatchEvent, error) {
	var w clubprofile.WatchEvent
	var raw string
	var fetched int64
	err := row.Scan(&w.ID, &w.ChatID, &w.TMDBID, &w.Date, &w.DatePrecision, &w.Zone, &w.Mode, &w.State, &w.Revision, &w.Film.Title, &w.Film.OriginalTitle, &w.Film.Year, &raw, &w.Film.Source, &fetched)
	if errors.Is(err, sql.ErrNoRows) {
		return w, clubprofile.ErrNotFound
	}
	if err != nil {
		return w, err
	}
	w.Film.TMDBID, w.Film.FetchedAt = w.TMDBID, time.Unix(fetched, 0)
	err = json.Unmarshal([]byte(raw), &w.Film.Metadata)
	return w, err
}

func (s *Store) ProfileWatches(ctx context.Context, chat int64) ([]clubprofile.WatchEvent, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+watchColumns+" FROM club_watches w JOIN club_films f ON f.tmdb_id=w.tmdb_id WHERE w.chat_id=? ORDER BY w.id", chat)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []clubprofile.WatchEvent
	for rows.Next() {
		w, e := scanProfileWatch(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func scanReviewSession(row interface{ Scan(...any) error }) (clubprofile.ReviewSession, error) {
	var s clubprofile.ReviewSession
	var raw string
	var finish int64
	err := row.Scan(&s.ID, &s.ChatID, &s.WatchID, &raw, &s.State, &s.Reason, &finish)
	if err != nil {
		return s, err
	}
	err = json.Unmarshal([]byte(raw), &s.Plan)
	if finish != 0 {
		s.FinishBefore = time.Unix(finish, 0)
	}
	return s, err
}

const reviewSessionColumns = `id,chat_id,watch_id,plan_json,state,reason,finish_before`

func (s *Store) ProfileSessions(ctx context.Context, chat int64) ([]clubprofile.ReviewSession, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+reviewSessionColumns+" FROM club_review_sessions WHERE (?=0 OR chat_id=?) ORDER BY id", chat, chat)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []clubprofile.ReviewSession
	for rows.Next() {
		x, e := scanReviewSession(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func recordWatchAudit(ctx context.Context, tx *sql.Tx, w clubprofile.WatchEvent, reason string, now time.Time) error {
	raw, err := json.Marshal(w)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO club_watch_audit(watch_id,revision,snapshot_json,reason,changed_at) VALUES(?,?,?,?,?)", w.ID, w.Revision, raw, reason, now.Unix())
	return err
}

func (s *Store) ChangeProfileWatch(ctx context.Context, op, chat, id int64, action, value string, now time.Time) error {
	return s.transaction(ctx, &op, func(tx *sql.Tx) error {
		w, err := scanProfileWatch(tx.QueryRowContext(ctx, "SELECT "+watchColumns+" FROM club_watches w JOIN club_films f ON f.tmdb_id=w.tmdb_id WHERE w.id=? AND w.chat_id=?", id, chat))
		if err != nil {
			return err
		}
		if err = changeWatchValue(&w, action, value, now); err != nil {
			return err
		}
		w.Revision++
		_, err = tx.ExecContext(ctx, "UPDATE club_watches SET watched_date=NULLIF(?,''),date_precision=?,state=?,revision=? WHERE id=?", w.Date, w.DatePrecision, w.State, w.Revision, w.ID)
		if err != nil {
			return err
		}
		if err = recordWatchAudit(ctx, tx, w, action, now); err != nil {
			return err
		}
		return cancelWatchReview(ctx, tx, w.ID, action)
	})
}

func changeWatchValue(w *clubprofile.WatchEvent, action, value string, now time.Time) error {
	if w.State != "active" {
		return clubprofile.ErrConflict
	}
	switch action {
	case "annul":
		w.State = "annulled"
	case "pause":
	case "date":
		in, err := clubprofile.ParseWatchInput("film | "+value, w.Zone, now)
		if err != nil {
			return err
		}
		w.Date, w.DatePrecision = in.Date, in.Precision
	default:
		return clubprofile.ErrInvalid
	}
	return nil
}

func cancelWatchReview(ctx context.Context, tx *sql.Tx, watch int64, reason string) error {
	if err := touchWatchCard(ctx, tx, watch); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, "UPDATE club_review_sessions SET state='cancelled',reason=? WHERE watch_id=? AND state IN ('queued','researching','blocked')", reason, watch)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "UPDATE club_profile_polls SET state='cancelled',error_code=? WHERE state='planned' AND session_id IN (SELECT id FROM club_review_sessions WHERE watch_id=?)", reason, watch)
	return err
}

func (s *Store) SetAutoReview(ctx context.Context, op, chat int64, enabled bool, now time.Time) error {
	return s.transaction(ctx, &op, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO club_profile_settings(chat_id,auto_review) VALUES(?,?) ON CONFLICT(chat_id) DO UPDATE SET auto_review=excluded.auto_review", chat, enabled)
		if err != nil || enabled {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE club_review_sessions SET state='cancelled',reason='auto_review_off' WHERE chat_id=? AND state IN ('queued','researching','blocked')", chat)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE club_profile_polls SET state='cancelled',error_code='auto_review_off',next_attempt=? WHERE chat_id=? AND topic='post_watch' AND state='planned'", now.Unix(), chat)
		return err
	})
}

package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/vaporon4a/movie-helper/internal/clubprofile"
)

const pendingState = "pending"

const watchDraftColumns = `id,chat_id,COALESCE(watch_id,0),input_json,candidates_json,state,reason,expires_at,next_attempt,message_id,version,rendered,card_closed`

func scanWatchDraft(row interface{ Scan(...any) error }) (clubprofile.WatchDraft, error) {
	var d clubprofile.WatchDraft
	var input, candidates string
	var expires, next int64
	err := row.Scan(&d.ID, &d.ChatID, &d.WatchID, &input, &candidates, &d.State, &d.Reason, &expires, &next, &d.MessageID, &d.Version, &d.Rendered, &d.Closed)
	if errors.Is(err, sql.ErrNoRows) {
		return d, clubprofile.ErrNotFound
	}
	if err != nil {
		return d, err
	}
	if err = json.Unmarshal([]byte(input), &d.Input); err != nil {
		return d, err
	}
	if err = json.Unmarshal([]byte(candidates), &d.Candidates); err != nil {
		return d, err
	}
	d.Expires, d.NextAttempt = time.Unix(expires, 0), time.Unix(next, 0)
	return d, nil
}

func (s *Store) ProfileZone(ctx context.Context, chat int64) (string, error) {
	var zone string
	err := s.db.QueryRowContext(ctx, "SELECT zone FROM chats WHERE chat_id=? AND active=1", chat).Scan(&zone)
	return zone, err
}

func (s *Store) CreateWatchDraft(ctx context.Context, op, chat int64, in clubprofile.WatchInput, now time.Time) (clubprofile.WatchDraft, error) {
	raw, err := json.Marshal(in)
	if err != nil {
		return clubprofile.WatchDraft{}, err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO club_watch_drafts(chat_id,operation_id,input_json,expires_at,next_attempt) VALUES(?,?,?,?,?) ON CONFLICT(operation_id) DO NOTHING`, chat, op, raw, now.Add(24*time.Hour).Unix(), now.Unix())
	if err != nil {
		return clubprofile.WatchDraft{}, err
	}
	return scanWatchDraft(s.db.QueryRowContext(ctx, "SELECT "+watchDraftColumns+" FROM club_watch_drafts WHERE operation_id=? AND chat_id=?", op, chat))
}

func (s *Store) WatchDraft(ctx context.Context, chat, id int64) (clubprofile.WatchDraft, error) {
	return scanWatchDraft(s.db.QueryRowContext(ctx, "SELECT "+watchDraftColumns+" FROM club_watch_drafts WHERE chat_id=? AND id=?", chat, id))
}

func (s *Store) BindWatchMessage(ctx context.Context, chat, id int64, message int) error {
	if message <= 0 {
		return clubprofile.ErrInvalid
	}
	r, e := s.db.ExecContext(ctx, "UPDATE club_watch_drafts SET message_id=? WHERE chat_id=? AND id=? AND message_id=-1", message, chat, id)
	return profileChanged(r, e)
}

func (s *Store) ClaimWatchMessage(ctx context.Context, chat, id int64, now time.Time) (bool, error) {
	r, err := s.db.ExecContext(ctx, "UPDATE club_watch_drafts SET message_id=-1 WHERE chat_id=? AND id=? AND message_id=0 AND card_next_attempt<=? AND EXISTS(SELECT 1 FROM chats WHERE chat_id=? AND active=1)", chat, id, now.Unix(), chat)
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	return n == 1, err
}

func (s *Store) ChooseWatchFilm(ctx context.Context, chat, id, film int64, now time.Time) (clubprofile.WatchDraft, error) {
	var d clubprofile.WatchDraft
	err := s.transaction(ctx, nil, func(tx *sql.Tx) error {
		var err error
		d, err = scanWatchDraft(tx.QueryRowContext(ctx, "SELECT "+watchDraftColumns+" FROM club_watch_drafts WHERE chat_id=? AND id=?", chat, id))
		if err != nil {
			return err
		}
		if d.State == "saved" {
			return nil
		}
		if d.State != "choice" || !now.Before(d.Expires) || !slices.ContainsFunc(d.Candidates, func(f clubprofile.Film) bool { return f.TMDBID == film }) {
			return clubprofile.ErrConflict
		}
		d.Input.TMDBID, d.Input.Query, d.State = film, "", pendingState
		d.Version++
		raw, err := json.Marshal(d.Input)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE club_watch_drafts SET input_json=?,state='pending',version=version+1,next_attempt=? WHERE id=?", raw, now.Unix(), id)
		return err
	})
	return d, err
}

func (s *Store) DueWatchDrafts(ctx context.Context, chat int64, now time.Time) ([]clubprofile.WatchDraft, error) {
	_, err := s.db.ExecContext(ctx, "UPDATE club_watch_drafts SET state='expired',reason='deadline',version=version+1 WHERE chat_id=? AND state IN ('pending','choice') AND expires_at<=?", chat, now.Unix())
	if err != nil {
		return nil, err
	}
	return s.watchDraftList(ctx, chat, now.Unix(), false)
}

func (s *Store) watchDraftList(ctx context.Context, chat, now int64, cards bool) ([]clubprofile.WatchDraft, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+watchDraftColumns+" FROM club_watch_drafts WHERE chat_id=? AND EXISTS(SELECT 1 FROM chats WHERE chat_id=? AND active=1) AND ((?=1 AND card_closed=0 AND ((message_id>0 AND rendered<version) OR (message_id=0 AND card_next_attempt>0 AND card_next_attempt<=?))) OR (?=0 AND state='pending' AND next_attempt<=?)) ORDER BY id LIMIT 5", chat, chat, cards, now, cards, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []clubprofile.WatchDraft
	for rows.Next() {
		d, e := scanWatchDraft(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) ClaimWatchDraft(ctx context.Context, id int64, now time.Time) (bool, error) {
	r, err := s.db.ExecContext(ctx, `UPDATE club_watch_drafts SET next_attempt=? WHERE id=? AND state='pending' AND next_attempt<=? AND expires_at>? AND EXISTS(SELECT 1 FROM chats WHERE chats.chat_id=club_watch_drafts.chat_id AND active=1)`, now.Add(time.Minute).Unix(), id, now.Unix(), now.Unix())
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	return n == 1, err
}

func (s *Store) FinishWatchResolution(ctx context.Context, d clubprofile.WatchDraft, films []clubprofile.Film, reason string, now time.Time) (clubprofile.WatchDraft, error) {
	err := s.transaction(ctx, nil, func(tx *sql.Tx) error {
		current, err := scanWatchDraft(tx.QueryRowContext(ctx, "SELECT "+watchDraftColumns+" FROM club_watch_drafts WHERE id=? AND chat_id=?", d.ID, d.ChatID))
		if err != nil {
			return err
		}
		if current.Version != d.Version || current.State != pendingState {
			return clubprofile.ErrConflict
		}
		d = current
		if err = resolveWatchDraft(ctx, tx, &d, films, reason, now); err != nil {
			return err
		}
		raw, err := json.Marshal(d.Candidates)
		if err != nil {
			return err
		}
		d.Version++
		_, err = tx.ExecContext(ctx, `UPDATE club_watch_drafts SET candidates_json=?,state=?,reason=?,watch_id=NULLIF(?,0),next_attempt=?,version=? WHERE id=?`, raw, d.State, d.Reason, d.WatchID, now.Add(5*time.Minute).Unix(), d.Version, d.ID)
		return err
	})
	return d, err
}

func resolveWatchDraft(ctx context.Context, tx *sql.Tx, d *clubprofile.WatchDraft, films []clubprofile.Film, reason string, now time.Time) error {
	if !now.Before(d.Expires) {
		d.State, d.Reason = "expired", "deadline"
		return nil
	}
	if reason != "" {
		d.Reason = reason
		return nil
	}
	if len(films) == 0 {
		d.State, d.Reason = "not_found", "no_matches"
		return nil
	}
	if d.Input.TMDBID == 0 {
		d.State, d.Candidates = "choice", films
		return nil
	}
	if len(films) != 1 || films[0].TMDBID != d.Input.TMDBID || films[0].Title == "" || films[0].Source != "tmdb" {
		return clubprofile.ErrInvalid
	}
	id, err := saveProfileWatch(ctx, tx, *d, films[0], now)
	if err != nil {
		return err
	}
	d.WatchID, d.State, d.Reason, d.Candidates = id, "saved", "", films
	return nil
}

func (s *Store) WatchCards(ctx context.Context, chat int64, now time.Time) ([]clubprofile.WatchDraft, error) {
	return s.watchDraftList(ctx, chat, now.Unix(), true)
}

func (s *Store) WatchCardRendered(ctx context.Context, id int64, version int) error {
	_, err := s.db.ExecContext(ctx, "UPDATE club_watch_drafts SET rendered=MAX(rendered,?) WHERE id=?", version, id)
	return err
}

// A closed panel is UI state only. Draft resolution, the viewing, its research
// session and all answers continue independently.
func (s *Store) CloseWatchCard(ctx context.Context, chat int64, message int) error {
	_, err := s.db.ExecContext(ctx, "UPDATE club_watch_drafts SET card_closed=1 WHERE chat_id=? AND message_id=?", chat, message)
	return err
}

// Only a confirmed rejection may release a send reservation. Ambiguous sends stay at -1.
func (s *Store) RetryWatchMessage(ctx context.Context, chat, id int64, next time.Time) error {
	r, err := s.db.ExecContext(ctx, "UPDATE club_watch_drafts SET message_id=0,card_next_attempt=? WHERE chat_id=? AND id=? AND message_id=-1", next.Unix(), chat, id)
	return profileChanged(r, err)
}

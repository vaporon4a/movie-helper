package storage

import (
	"context"
	"database/sql"
	"errors"
	"github.com/vaporon4a/movie-helper/internal/clubprofile"
	"time"
)

func (s *Store) ProfilePreparationStates(ctx context.Context, chat int64) ([]clubprofile.PreparationState, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT w.id,r.id,w.revision,j.watch_revision,j.mode,COALESCE(f.mode,'off'),j.stage,j.updated_at,EXISTS(SELECT 1 FROM film_research_approvals a WHERE a.preparation_id=j.preparation_id)
 FROM club_watches w JOIN club_review_sessions r ON r.watch_id=w.id AND r.chat_id=w.chat_id
 JOIN film_research_jobs j ON j.session_id=r.id AND j.chat_id=w.chat_id
 LEFT JOIN film_research_settings f ON f.chat_id=w.chat_id WHERE w.chat_id=?`, chat)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []clubprofile.PreparationState
	for rows.Next() {
		var p clubprofile.PreparationState
		var updated int64
		if err = rows.Scan(&p.WatchID, &p.SessionID, &p.WatchRevision, &p.PreparedRevision, &p.EnrollmentMode, &p.EffectiveMode, &p.Stage, &updated, &p.Approved); err != nil {
			return nil, err
		}
		p.UpdatedAt = time.Unix(updated, 0)
		out = append(out, p)
	}
	return out, rows.Err()
}
func (s *Store) SaveProfilePage(ctx context.Context, chat int64, message int, page string, id int64) error {
	_, err := s.db.ExecContext(ctx, "UPDATE club_watch_drafts SET card_page=?,card_page_id=?,card_page_revision=COALESCE((SELECT revision FROM club_watches WHERE id=? AND chat_id=?),0) WHERE chat_id=? AND message_id=? AND card_closed=0", page, id, id, chat, chat, message)
	return err
}
func (s *Store) ProfilePage(ctx context.Context, chat int64, message int) (string, int64, bool, error) {
	var page string
	var id int64
	var closed bool
	err := s.db.QueryRowContext(ctx, "SELECT card_page,card_page_id,card_closed FROM club_watch_drafts WHERE chat_id=? AND message_id=?", chat, message).Scan(&page, &id, &closed)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, false, nil
	}
	return page, id, closed, err
}
func nextReviewSession(ctx context.Context, tx *sql.Tx, chat int64) (clubprofile.ReviewSession, error) {
	rows, err := tx.QueryContext(ctx, "SELECT "+reviewSessionColumns+" FROM club_review_sessions WHERE chat_id=? AND state IN ('queued','researching','blocked')", chat)
	if err != nil {
		return clubprofile.ReviewSession{}, err
	}
	defer rows.Close()
	var sessions []clubprofile.ReviewSession
	for rows.Next() {
		s, e := scanReviewSession(rows)
		if e != nil {
			return s, e
		}
		sessions = append(sessions, s)
	}
	if err = rows.Err(); err != nil {
		return clubprofile.ReviewSession{}, err
	}
	q := clubprofile.ReviewQueue(sessions)
	if len(q) == 0 {
		return clubprofile.ReviewSession{}, sql.ErrNoRows
	}
	return q[0], nil
}

func (s *Store) ProfilePageRevision(ctx context.Context, chat int64, message int) (int, error) {
	var revision int
	err := s.db.QueryRowContext(ctx, "SELECT card_page_revision FROM club_watch_drafts WHERE chat_id=? AND message_id=?", chat, message).Scan(&revision)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return revision, err
}

package storage

import (
	"context"
	"database/sql"

	"github.com/vaporon4a/movie-helper/internal/clubprofile"
)

func (s *Store) StatisticsInput(ctx context.Context, chat int64) (clubprofile.StatisticsInput, error) {
	in := clubprofile.StatisticsInput{ChatID: chat}
	if chat == 0 {
		return in, clubprofile.ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return in, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = tx.QueryRowContext(ctx, "SELECT zone FROM chats WHERE chat_id=?", chat).Scan(&in.Zone); err != nil {
		return in, err
	}
	in.Watches, err = statisticsRows(ctx, tx, "SELECT "+watchColumns+" FROM club_watches w JOIN club_films f ON f.tmdb_id=w.tmdb_id WHERE w.chat_id=? ORDER BY w.id", chat, func(r *sql.Rows) (clubprofile.WatchEvent, error) { return scanProfileWatch(r) })
	if err != nil {
		return in, err
	}
	in.Sessions, err = statisticsRows(ctx, tx, "SELECT "+reviewSessionColumns+" FROM club_review_sessions WHERE chat_id=? ORDER BY id", chat, func(r *sql.Rows) (clubprofile.ReviewSession, error) { return scanReviewSession(r) })
	if err != nil {
		return in, err
	}
	in.Polls, err = statisticsRows(ctx, tx, "SELECT "+profilePollColumns+" FROM club_profile_polls WHERE chat_id=? ORDER BY id", chat, func(r *sql.Rows) (clubprofile.Poll, error) { return scanProfilePoll(r) })
	if err != nil {
		return in, err
	}
	return in, tx.Commit()
}
func statisticsRows[T any](ctx context.Context, tx *sql.Tx, query string, chat int64, scan func(*sql.Rows) (T, error)) ([]T, error) {
	rows, err := tx.QueryContext(ctx, query, chat)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []T
	for rows.Next() {
		v, e := scan(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

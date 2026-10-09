package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/vaporon4a/movie-helper/internal/facts"
)

func (s *Store) LoadFactCache(ctx context.Context, key string, now time.Time) ([]byte, bool, error) {
	var data []byte
	err := s.db.QueryRowContext(ctx, "SELECT payload FROM fact_source_cache WHERE cache_key=? AND expires_at>?", key, now.Unix()).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	return data, err == nil, err
}
func (s *Store) SaveFactCache(ctx context.Context, key string, data []byte, expires time.Time) error {
	if len(data) > 2<<20 {
		return errors.New("fact cache too large")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO fact_source_cache(cache_key,payload,expires_at) VALUES(?,?,?)
 ON CONFLICT(cache_key) DO UPDATE SET payload=excluded.payload,expires_at=excluded.expires_at`, key, data, expires.Unix())
	return err
}

// Persist attempts separately from publication history; failed/no-story movies rotate too.
func (s *Store) PickFactMovies(ctx context.Context, chat int64, movies []facts.Movie, now time.Time, limit int) ([]facts.Movie, error) {
	var out []facts.Movie
	err := s.transaction(ctx, nil, func(tx *sql.Tx) error {
		for _, movie := range movies {
			var recent bool
			err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM fact_source_attempts WHERE chat_id=? AND movie_id=? AND checked_at>?)
 OR EXISTS(SELECT 1 FROM items WHERE chat_id=? AND content_key LIKE ? AND (state IN ('approved','pending','reserved') OR created_at>?))`,
				chat, movie.ID, now.Add(-24*time.Hour).Unix(), chat, fmt.Sprintf("factru:%d:%%", movie.ID), now.Add(-30*24*time.Hour).Unix()).Scan(&recent)
			if err != nil {
				return err
			}
			if recent {
				continue
			}
			out = append(out, movie)
			_, err = tx.ExecContext(ctx, `INSERT INTO fact_source_attempts(chat_id,movie_id,checked_at) VALUES(?,?,?)
 ON CONFLICT(chat_id,movie_id) DO UPDATE SET checked_at=excluded.checked_at`, chat, movie.ID, now.Unix())
			if err != nil {
				return err
			}
			if len(out) >= limit {
				break
			}
		}
		return nil
	})
	return out, err
}

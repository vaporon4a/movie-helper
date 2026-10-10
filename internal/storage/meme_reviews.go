package storage

import (
	"context"
	"database/sql"
	"time"
)

func (s *Store) MemeRejected(ctx context.Context, scope, key string, now time.Time) (bool, error) {
	var rejected bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM meme_rejections WHERE scope=? AND source_key=? AND expires_at>?)`, scope, key, now.Unix()).Scan(&rejected)
	return rejected, err
}

func (s *Store) RejectMeme(ctx context.Context, scope, key string, now time.Time) error {
	return s.transaction(ctx, nil, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM meme_rejections WHERE expires_at<=?`, now.Unix()); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO meme_rejections(scope,source_key,expires_at) VALUES(?,?,?) ON CONFLICT(scope,source_key) DO UPDATE SET expires_at=excluded.expires_at`, scope, key, now.Add(24*time.Hour).Unix())
		return err
	})
}

// Reserve is kept for audit, never counted as publishable stock.
func (s *Store) RejectMemeReview(ctx context.Context, scope, key, decision, assessment string, now time.Time) error {
	return s.transaction(ctx, nil, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM meme_rejections WHERE expires_at<=?`, now.Unix()); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO meme_rejections(scope,source_key,expires_at,decision,assessment) VALUES(?,?,?,?,?)
 ON CONFLICT(scope,source_key) DO UPDATE SET expires_at=excluded.expires_at,decision=excluded.decision,assessment=excluded.assessment`, scope, key, now.Add(24*time.Hour).Unix(), decision, assessment)
		return err
	})
}

package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/vaporon4a/movie-helper/internal/clubprofile"
)

func (s *Store) DueFilmEnrichment(ctx context.Context, now time.Time) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT tmdb_id FROM club_film_enrichment WHERE complete=0 AND attempts<6 AND next_attempt<=? ORDER BY next_attempt,tmdb_id LIMIT 2", now.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) SaveFilmEnrichment(ctx context.Context, id int64, f *clubprofile.Film, now time.Time) error {
	return s.transaction(ctx, nil, func(tx *sql.Tx) error {
		complete := f != nil && f.Metadata.Complete
		if f != nil {
			raw, err := json.Marshal(f.Metadata)
			if err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, "UPDATE club_films SET metadata_json=?,fetched_at=? WHERE tmdb_id=?", raw, f.FetchedAt.Unix(), id); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, "UPDATE club_watch_drafts SET version=version+1 WHERE watch_id IN (SELECT id FROM club_watches WHERE tmdb_id=?)", id); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, "UPDATE club_film_enrichment SET attempts=attempts+1,complete=?,next_attempt=? WHERE tmdb_id=?", complete, now.Add(6*time.Hour).Unix(), id)
		return err
	})
}

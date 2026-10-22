package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/vaporon4a/movie-helper/internal/filmresearch"
)

var _ filmresearch.Repository = (*Store)(nil)

// SaveResearchPreparation stores candidates only. It does not update the review
// plan, start the clock, enqueue delivery, or declare semantic approval.
func (s *Store) SaveResearchPreparation(ctx context.Context, b filmresearch.Bundle, now time.Time) (string, error) {
	if err := b.Validate(); err != nil {
		return "", err
	}
	if now.IsZero() {
		return "", filmresearch.ErrInvalid
	}
	p := b.Preparation
	id, err := p.ID()
	if err != nil {
		return "", err
	}
	err = s.transaction(ctx, nil, func(tx *sql.Tx) error {
		var exists bool
		if e := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM film_research_preparations WHERE id=? AND chat_id=?)", id, p.ChatID).Scan(&exists); e != nil || exists {
			// An identical retry is a no-op, even if delivery has since begun.
			return e
		}
		if e := checkResearchSession(ctx, tx, p); e != nil {
			return e
		}
		if e := saveResearchEvidence(ctx, tx, b, now); e != nil {
			return e
		}
		raw, e := json.Marshal(p)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO film_research_preparations
 (id,chat_id,session_id,watch_id,watch_revision,tmdb_id,dossier_id,policy_version,snapshot_json,created_at)
 VALUES(?,?,?,?,?,?,?,?,?,?)`, id, p.ChatID, p.SessionID, p.WatchID, p.WatchRevision, p.TMDBID, p.DossierID, p.PolicyVersion, raw, now.Unix())
		return e
	})
	if err != nil {
		return "", err
	}
	return id, nil
}

func checkResearchSession(ctx context.Context, tx *sql.Tx, p filmresearch.Preparation) error {
	var eligible bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(
 SELECT 1 FROM club_review_sessions r JOIN club_watches w ON w.id=r.watch_id AND w.chat_id=r.chat_id
 WHERE r.id=? AND r.chat_id=? AND r.watch_id=? AND w.revision=? AND w.tmdb_id=? AND w.state='active'
 AND r.state IN ('queued','researching')
 AND COALESCE((SELECT auto_review FROM club_profile_settings WHERE chat_id=r.chat_id),1)=1
 AND NOT EXISTS(SELECT 1 FROM club_profile_polls p WHERE p.session_id=r.id))`, p.SessionID, p.ChatID, p.WatchID, p.WatchRevision, p.TMDBID).Scan(&eligible)
	if err != nil {
		return err
	}
	if !eligible {
		return filmresearch.ErrConflict
	}
	return nil
}

func saveResearchEvidence(ctx context.Context, tx *sql.Tx, b filmresearch.Bundle, now time.Time) error {
	for i, source := range b.Sources {
		raw, err := json.Marshal(source)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO film_research_sources(id,tmdb_id,snapshot_json,created_at) VALUES(?,?,?,?) ON CONFLICT(id) DO NOTHING`, b.Dossier.SourceIDs[i], source.TMDBID, raw, now.Unix()); err != nil {
			return err
		}
	}
	raw, err := json.Marshal(b.Dossier)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO film_research_dossiers(id,tmdb_id,snapshot_json,created_at) VALUES(?,?,?,?) ON CONFLICT(id) DO NOTHING`, b.Preparation.DossierID, b.Dossier.TMDBID, raw, now.Unix()); err != nil {
		return err
	}
	for _, id := range b.Dossier.SourceIDs {
		if _, err = tx.ExecContext(ctx, `INSERT INTO film_research_dossier_sources(dossier_id,source_id,tmdb_id) VALUES(?,?,?) ON CONFLICT(dossier_id,source_id) DO NOTHING`, b.Preparation.DossierID, id, b.Dossier.TMDBID); err != nil {
			return err
		}
	}
	return nil
}

// ResearchPreparation reads historical evidence within one chat. A successful
// read is not proof that a session is still eligible to publish this artifact.
func (s *Store) ResearchPreparation(ctx context.Context, chat int64, id string) (filmresearch.Bundle, error) {
	var b filmresearch.Bundle
	var preparation, dossier string
	err := s.db.QueryRowContext(ctx, `SELECT p.snapshot_json,d.snapshot_json FROM film_research_preparations p
 JOIN film_research_dossiers d ON d.id=p.dossier_id AND d.tmdb_id=p.tmdb_id WHERE p.id=? AND p.chat_id=?`, id, chat).Scan(&preparation, &dossier)
	if errors.Is(err, sql.ErrNoRows) {
		return b, filmresearch.ErrNotFound
	}
	if err != nil {
		return b, err
	}
	if err = json.Unmarshal([]byte(preparation), &b.Preparation); err != nil {
		return b, err
	}
	if err = json.Unmarshal([]byte(dossier), &b.Dossier); err != nil {
		return b, err
	}
	if err = s.readResearchSources(ctx, &b); err != nil {
		return filmresearch.Bundle{}, err
	}
	if err = b.Validate(); err != nil {
		return filmresearch.Bundle{}, err
	}
	actual, err := b.Preparation.ID()
	if err != nil {
		return filmresearch.Bundle{}, err
	}
	if actual != id || b.Preparation.ChatID != chat {
		return filmresearch.Bundle{}, filmresearch.ErrInvalid
	}
	return b, nil
}

func (s *Store) readResearchSources(ctx context.Context, b *filmresearch.Bundle) error {
	// Four questions need a bounded dossier (at most 16 sources); these reads
	// preserve the recorded source ordering without a mutable cache dependency.
	for _, id := range b.Dossier.SourceIDs {
		var raw string
		err := s.db.QueryRowContext(ctx, `SELECT s.snapshot_json FROM film_research_sources s
 JOIN film_research_dossier_sources l ON l.source_id=s.id AND l.tmdb_id=s.tmdb_id
 WHERE l.dossier_id=? AND s.id=? AND s.tmdb_id=?`, b.Preparation.DossierID, id, b.Dossier.TMDBID).Scan(&raw)
		if err != nil {
			return err
		}
		var source filmresearch.SourceSnapshot
		if err = json.Unmarshal([]byte(raw), &source); err != nil {
			return err
		}
		b.Sources = append(b.Sources, source)
	}
	return nil
}

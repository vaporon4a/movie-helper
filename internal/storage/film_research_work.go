package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/vaporon4a/movie-helper/internal/aiwork"
	"github.com/vaporon4a/movie-helper/internal/clubprofile"
	"github.com/vaporon4a/movie-helper/internal/filmresearch"
)

const researchReady = "ready"

// ConfigureFilmResearch affects enrollment of future sessions only. Disabling
// also prevents ready bundles being adopted, without mutating frozen questions.
func (s *Store) ConfigureFilmResearch(ctx context.Context, mode string, chats map[int64]bool) error {
	if mode != "off" && mode != "shadow" && mode != "on" {
		return filmresearch.ErrInvalid
	}
	return s.transaction(ctx, nil, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "UPDATE film_research_settings SET mode='off'"); err != nil {
			return err
		}
		for chat, allowed := range chats {
			if !allowed {
				continue
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO film_research_settings(chat_id,mode) VALUES(?,?) ON CONFLICT(chat_id) DO UPDATE SET mode=excluded.mode`, chat, mode); err != nil {
				return err
			}
		}
		return retireResearchJobs(ctx, tx)
	})
}
func enrollResearch(ctx context.Context, tx *sql.Tx, w clubprofile.WatchEvent, now time.Time) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO film_research_jobs(session_id,chat_id,watch_id,watch_revision,mode,updated_at)
 SELECT r.id,r.chat_id,r.watch_id,?,s.mode,? FROM club_review_sessions r JOIN film_research_settings s USING(chat_id)
 WHERE r.watch_id=? AND r.chat_id=? AND s.mode IN ('shadow','on') ON CONFLICT(session_id) DO NOTHING`, w.Revision, now.Unix(), w.ID, w.ChatID)
	return err
}
func seedResearchWork(ctx context.Context, tx *sql.Tx, now time.Time) error {
	if err := retireResearchJobs(ctx, tx); err != nil {
		return err
	}
	// Rank all unfinished sessions, including ready bundles: at most the first
	// two waiting films per chat consume background generation capacity.
	_, err := tx.ExecContext(ctx, `WITH ranked AS (
 SELECT j.session_id,j.stage,row_number() OVER(PARTITION BY j.chat_id ORDER BY j.session_id) AS n
 FROM film_research_jobs j JOIN club_review_sessions r ON r.id=j.session_id
 JOIN club_watches w ON w.id=j.watch_id JOIN chats c ON c.chat_id=j.chat_id
 JOIN film_research_settings s ON s.chat_id=j.chat_id
 WHERE r.state IN ('queued','researching') AND w.state='active' AND w.revision=j.watch_revision
 AND c.active=1 AND s.mode!='off' AND j.stage IN ('sources','generate','audit','ready')
 AND COALESCE((SELECT auto_review FROM club_profile_settings WHERE chat_id=j.chat_id),1)=1
 AND NOT EXISTS(SELECT 1 FROM club_profile_polls p WHERE p.session_id=j.session_id))
 INSERT INTO ai_work(kind,scope_id,work_key,state,priority,next_attempt,created_at,updated_at)
 SELECT ?,session_id,'film_research:'||session_id,'pending',?,?,?,? FROM ranked
 WHERE n<=2 AND stage!='ready' ON CONFLICT(work_key) DO NOTHING`, aiwork.FilmResearch, aiwork.PriorityBackground, now.Unix(), now.Unix(), now.Unix())
	return err
}
func (s *Store) ResearchJob(ctx context.Context, id int64) (filmresearch.Job, error) {
	var j filmresearch.Job
	var raw, audit string
	err := s.db.QueryRowContext(ctx, `SELECT j.session_id,j.chat_id,j.watch_id,j.watch_revision,j.stage,j.version,j.repairs,j.bundle_json,j.audit_json,w.tmdb_id,f.title,f.original_title,COALESCE(f.release_year,0)
 FROM film_research_jobs j JOIN club_watches w ON w.id=j.watch_id JOIN club_films f ON f.tmdb_id=w.tmdb_id
 JOIN club_review_sessions r ON r.id=j.session_id JOIN film_research_settings s ON s.chat_id=j.chat_id
 JOIN chats c ON c.chat_id=j.chat_id
 WHERE j.session_id=? AND w.state='active' AND w.revision=j.watch_revision AND r.state IN ('queued','researching')
 AND c.active=1 AND s.mode!='off' AND COALESCE((SELECT auto_review FROM club_profile_settings WHERE chat_id=j.chat_id),1)=1
 AND NOT EXISTS(SELECT 1 FROM club_profile_polls p WHERE p.session_id=j.session_id)`, id).Scan(&j.SessionID, &j.ChatID, &j.WatchID, &j.WatchRevision, &j.Stage, &j.Version, &j.Repairs, &raw, &audit, &j.Film.TMDBID, &j.Film.Title, &j.Film.OriginalTitle, &j.Film.Year)
	if errors.Is(err, sql.ErrNoRows) {
		return j, filmresearch.ErrConflict
	}
	if err != nil {
		return j, err
	}
	if err = json.Unmarshal([]byte(raw), &j.Bundle); err != nil {
		return j, err
	}
	err = json.Unmarshal([]byte(audit), &j.Audit)
	return j, err
}
func (s *Store) CheckpointResearch(ctx context.Context, j filmresearch.Job, next string, now time.Time) error {
	preparation, err := s.validateResearchCheckpoint(ctx, j, next, now)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(j.Bundle)
	if err != nil {
		return err
	}
	audit, err := json.Marshal(j.Audit)
	if err != nil {
		return err
	}
	return s.transaction(ctx, nil, func(tx *sql.Tx) error {
		p := filmresearch.Preparation{SessionID: j.SessionID, ChatID: j.ChatID, WatchID: j.WatchID, WatchRevision: j.WatchRevision, TMDBID: j.Film.TMDBID}
		if e := checkResearchSession(ctx, tx, p); e != nil {
			return e
		}
		if next == researchReady {
			if e := saveResearchApproval(ctx, tx, j, raw, audit, preparation, now); e != nil {
				return e
			}
		}
		r, e := tx.ExecContext(ctx, `UPDATE film_research_jobs SET stage=?,version=version+1,repairs=?,bundle_json=?,audit_json=?,preparation_id=NULLIF(?,''),updated_at=?
 WHERE session_id=? AND chat_id=? AND version=? AND stage=? AND watch_revision=?
 AND EXISTS(SELECT 1 FROM film_research_settings WHERE chat_id=? AND mode!='off')`, next, j.Repairs, raw, audit, preparation, now.Unix(), j.SessionID, j.ChatID, j.Version, j.Stage, j.WatchRevision, j.ChatID)
		if e != nil {
			return e
		}
		return researchChanged(r)
	})
}
func validResearchTransition(from, to string, repairs int) bool {
	if repairs < 0 || repairs > 1 {
		return false
	}
	switch from {
	case "sources":
		return to == "generate" || to == rejectedState
	case "generate":
		return to == "audit" || to == rejectedState || (to == "generate" && repairs == 1)
	case "audit":
		return to == researchReady || to == rejectedState || (to == "generate" && repairs == 1)
	default:
		return false
	}
}

func freezeResearchPlan(ctx context.Context, tx *sql.Tx, s *clubprofile.ReviewSession) error {
	var raw, audit, prep, mode string
	err := tx.QueryRowContext(ctx, `SELECT j.bundle_json,a.audit_json,COALESCE(j.preparation_id,''),s.mode
 FROM film_research_jobs j JOIN film_research_settings s USING(chat_id)
 JOIN film_research_approvals a ON a.preparation_id=j.preparation_id
 JOIN club_watches w ON w.id=j.watch_id AND w.chat_id=j.chat_id
 WHERE j.session_id=? AND j.chat_id=? AND j.stage='ready' AND j.mode='on' AND s.mode='on'
 AND w.state='active' AND w.revision=j.watch_revision`, s.ID, s.ChatID).Scan(&raw, &audit, &prep, &mode)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	next := "fallback"
	if err == nil {
		plan, e := validatedResearchPlan(raw, audit, prep)
		if e != nil {
			return e
		}
		planRaw, e := json.Marshal(plan)
		if e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "UPDATE club_review_sessions SET plan_json=? WHERE id=? AND chat_id=?", planRaw, s.ID, s.ChatID); e != nil {
			return e
		}
		s.Plan = plan
		next = "frozen"
	}
	_, err = tx.ExecContext(ctx, "UPDATE film_research_jobs SET stage=?,version=version+1 WHERE session_id=? AND stage NOT IN ('frozen','fallback')", next, s.ID)
	return err
}

func (s *Store) validateResearchCheckpoint(ctx context.Context, j filmresearch.Job, next string, now time.Time) (string, error) {
	if !validResearchTransition(j.Stage, next, j.Repairs) {
		return "", filmresearch.ErrInvalid
	}
	preparation := ""
	if next == "audit" || next == researchReady {
		if err := j.Bundle.Validate(); err != nil {
			return "", err
		}
		if j.Bundle.Preparation.SessionID != j.SessionID || j.Bundle.Preparation.WatchRevision != j.WatchRevision {
			return "", filmresearch.ErrInvalid
		}
	}
	if next == researchReady {
		if !j.Audit.GroundedAccepted(j.Bundle) || j.Audit.Provenance.Provider == "" || j.Audit.Provenance.Model == "" {
			return "", filmresearch.ErrInvalid
		}
		var err error
		preparation, err = s.SaveResearchPreparation(ctx, j.Bundle, now)
		if err != nil {
			return "", err
		}
	}
	return preparation, nil
}
func checkAuditedBundle(ctx context.Context, tx *sql.Tx, j filmresearch.Job, raw []byte) error {
	var previous string
	if err := tx.QueryRowContext(ctx, "SELECT bundle_json FROM film_research_jobs WHERE session_id=? AND version=?", j.SessionID, j.Version).Scan(&previous); err != nil {
		return err
	}
	if previous != string(raw) {
		return filmresearch.ErrConflict
	}
	return nil
}

func validatedResearchPlan(raw, audit, prep string) (clubprofile.ReviewPlan, error) {
	var b filmresearch.Bundle
	var a filmresearch.Audit
	if json.Unmarshal([]byte(raw), &b) != nil || json.Unmarshal([]byte(audit), &a) != nil || b.Validate() != nil || !a.GroundedAccepted(b) {
		return clubprofile.ReviewPlan{}, filmresearch.ErrInvalid
	}
	id, err := b.Preparation.ID()
	if err != nil {
		return clubprofile.ReviewPlan{}, err
	}
	if id != prep {
		return clubprofile.ReviewPlan{}, filmresearch.ErrInvalid
	}
	return clubprofile.ResearchReviewPlan(prep, b.Preparation.Questions)
}

func researchChanged(r sql.Result) error {
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return filmresearch.ErrConflict
	}
	return nil
}

func saveResearchApproval(ctx context.Context, tx *sql.Tx, j filmresearch.Job, raw, audit []byte, id string, now time.Time) error {
	if err := checkAuditedBundle(ctx, tx, j, raw); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, "INSERT INTO film_research_approvals(preparation_id,audit_json,created_at) VALUES(?,?,?) ON CONFLICT(preparation_id) DO NOTHING", id, audit, now.Unix())
	return err
}

// A cancelled or disabled preparation must not occupy the two-film FIFO window
// after re-enabling a chat. Already frozen series retain their snapshots.
func retireResearchJobs(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `UPDATE film_research_jobs SET stage='superseded',version=version+1
 WHERE stage IN ('sources','generate','audit','ready') AND NOT EXISTS(
 SELECT 1 FROM club_review_sessions r JOIN club_watches w ON w.id=r.watch_id
 JOIN film_research_settings s ON s.chat_id=r.chat_id
 WHERE r.id=film_research_jobs.session_id AND w.state='active' AND w.revision=film_research_jobs.watch_revision
 AND r.state IN ('queued','researching') AND s.mode!='off'
 AND COALESCE((SELECT auto_review FROM club_profile_settings WHERE chat_id=r.chat_id),1)=1
 AND NOT EXISTS(SELECT 1 FROM club_profile_polls p WHERE p.session_id=r.id))`)
	return err
}

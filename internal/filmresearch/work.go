package filmresearch

import (
	"context"
	"time"

	"github.com/vaporon4a/movie-helper/internal/clubprofile"
)

// Job contains no participant identity. Version is an optimistic checkpoint
// counter; the watch revision separately protects corrections and cancellations.
type Job struct {
	SessionID, ChatID, WatchID int64
	WatchRevision              int
	Film                       clubprofile.Film
	Stage                      string
	Version, Repairs           int
	Bundle                     Bundle
	Audit                      Audit
}
type WorkRepository interface {
	ResearchJob(context.Context, int64) (Job, error)
	CheckpointResearch(context.Context, Job, string, time.Time) error
}
type Source interface {
	ResearchSources(context.Context, clubprofile.Film, time.Time) ([]SourceSnapshot, error)
}

func NewBundle(j Job, sources []SourceSnapshot) (Bundle, error) {
	b := Bundle{Sources: sources, Dossier: Dossier{Version: DossierV1, TMDBID: j.Film.TMDBID, Title: j.Film.Title, Year: j.Film.Year}, Preparation: Preparation{Version: PreparationV1, ChatID: j.ChatID, SessionID: j.SessionID, WatchID: j.WatchID, WatchRevision: j.WatchRevision, TMDBID: j.Film.TMDBID, PolicyVersion: PolicyV1}}
	seen := map[string]bool{}
	bytes := 0
	b.Sources = nil
	for _, s := range sources {
		if err := s.Validate(); err != nil || s.TMDBID != j.Film.TMDBID {
			return Bundle{}, ErrInvalid
		}
		id, err := s.ID()
		if err != nil {
			return Bundle{}, err
		}
		if bytes+len(s.Text) > 18000 {
			continue
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		bytes += len(s.Text)
		b.Sources = append(b.Sources, s)
		b.Dossier.SourceIDs = append(b.Dossier.SourceIDs, id)
	}
	if len(b.Sources) == 0 || len(b.Sources) > 16 {
		return Bundle{}, ErrInvalid
	}
	id, err := b.Dossier.ID()
	b.Preparation.DossierID = id
	return b, err
}

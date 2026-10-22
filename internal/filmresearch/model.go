// Package filmresearch holds immutable evidence and candidate question bundles.
// Preparing a bundle never changes a review session or authorizes publication.
package filmresearch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/vaporon4a/movie-helper/internal/clubprofile"
)

const (
	SourceV1      = "film-source-v1"
	DossierV1     = "film-dossier-v1"
	PreparationV1 = "film-preparation-v1"
)

var (
	ErrInvalid  = errors.New("invalid film research snapshot")
	ErrConflict = errors.New("film research session changed or frozen")
	ErrNotFound = errors.New("film research snapshot not found")
)

// EvidenceSpan uses UTF-8 byte offsets into the immutable source text.
type EvidenceSpan struct {
	ID    string `json:"id"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

type SourceSnapshot struct {
	Version     string         `json:"version"`
	TMDBID      int64          `json:"tmdb_id"`
	Kind        string         `json:"kind"`
	URL         string         `json:"url"`
	Revision    string         `json:"revision,omitempty"`
	Language    string         `json:"language"`
	Attribution string         `json:"attribution"`
	License     string         `json:"license,omitempty"`
	Text        string         `json:"text"`
	Spans       []EvidenceSpan `json:"spans"`
	FetchedAt   time.Time      `json:"fetched_at"`
}

type Dossier struct {
	Version   string   `json:"version"`
	TMDBID    int64    `json:"tmdb_id"`
	Title     string   `json:"title"`
	Year      int      `json:"year"`
	SourceIDs []string `json:"source_ids"`
}

type Provenance struct {
	Temperature      *float64 `json:"temperature,omitempty"`
	ReasoningEffort  string   `json:"reasoning_effort,omitempty"`
	MaxOutputTokens  int      `json:"max_output_tokens,omitempty"`
	Provider         string   `json:"provider"`
	Model            string   `json:"model"`
	PromptVersion    string   `json:"prompt_version"`
	ValidatorVersion string   `json:"validator_version"`
}

type RejectedFamily struct {
	FamilyID string `json:"family_id"`
	Reason   string `json:"reason"`
}

type SelectionReceipt struct {
	PolicyVersion    string           `json:"policy_version"`
	Seed             string           `json:"seed"`
	EligibleFamilies []string         `json:"eligible_families"`
	SelectedFamilies []string         `json:"selected_families"`
	Rejected         []RejectedFamily `json:"rejected"`
}

type Preparation struct {
	Version       string                 `json:"version"`
	ChatID        int64                  `json:"chat_id"`
	SessionID     int64                  `json:"session_id"`
	WatchID       int64                  `json:"watch_id"`
	WatchRevision int                    `json:"watch_revision"`
	TMDBID        int64                  `json:"tmdb_id"`
	PolicyVersion string                 `json:"policy_version"`
	DossierID     string                 `json:"dossier_id"`
	Questions     []clubprofile.Question `json:"questions"`
	Generator     Provenance             `json:"generator"`
	Selection     SelectionReceipt       `json:"selection"`
}

// Bundle contains four addressed candidates, not the generic overall question.
// It is deliberately not a ready-to-publish artifact: semantic approval and
// schedule integration are separate steps.
type Bundle struct {
	Sources     []SourceSnapshot `json:"sources"`
	Dossier     Dossier          `json:"dossier"`
	Preparation Preparation      `json:"preparation"`
}

type Repository interface {
	SaveResearchPreparation(context.Context, Bundle, time.Time) (string, error)
	ResearchPreparation(context.Context, int64, string) (Bundle, error)
}

func snapshotID(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func (s SourceSnapshot) ID() (string, error) { return snapshotID(s) }
func (d Dossier) ID() (string, error)        { return snapshotID(d) }
func (p Preparation) ID() (string, error)    { return snapshotID(p) }

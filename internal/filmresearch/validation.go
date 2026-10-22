package filmresearch

import (
	"fmt"
	"github.com/vaporon4a/movie-helper/internal/clubprofile"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"
)

func (s SourceSnapshot) Validate() error {
	u, err := url.Parse(s.URL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" {
		return ErrInvalid
	}
	if s.Version != SourceV1 || s.TMDBID <= 0 || !slices.Contains([]string{"plot", "metadata", "opinion"}, s.Kind) || s.Language == "" || s.Attribution == "" || s.FetchedAt.IsZero() {
		return ErrInvalid
	}
	if strings.TrimSpace(s.Text) == "" || len(s.Text) > 32000 || !utf8.ValidString(s.Text) || len(s.Spans) == 0 || len(s.Spans) > 32 {
		return ErrInvalid
	}
	seen := make(map[string]bool, len(s.Spans))
	for _, span := range s.Spans {
		if span.ID == "" || seen[span.ID] || span.Start < 0 || span.End > len(s.Text) || span.Start >= span.End {
			return ErrInvalid
		}
		if !utf8.ValidString(s.Text[span.Start:span.End]) {
			return ErrInvalid
		}
		seen[span.ID] = true
	}
	return nil
}

func (b Bundle) Validate() error {
	p, d := b.Preparation, b.Dossier
	if p.Version != PreparationV1 || p.ChatID == 0 || p.SessionID <= 0 || p.WatchID <= 0 || p.WatchRevision < 1 || p.TMDBID <= 0 || p.PolicyVersion == "" {
		return ErrInvalid
	}
	if d.Version != DossierV1 || d.TMDBID != p.TMDBID || strings.TrimSpace(d.Title) == "" || d.Year < 1 || len(b.Sources) == 0 || len(b.Sources) > 16 {
		return ErrInvalid
	}
	id, err := d.ID()
	if err != nil {
		return err
	}
	if id != p.DossierID || len(d.SourceIDs) != len(b.Sources) || len(p.Questions) != 4 {
		return ErrInvalid
	}
	if p.Generator.Provider == "" || p.Generator.Model == "" || p.Generator.PromptVersion == "" || p.Generator.ValidatorVersion == "" {
		return ErrInvalid
	}
	sources, err := b.sourceIndex()
	if err != nil {
		return err
	}
	if err = p.validateQuestions(sources); err != nil {
		return err
	}
	return p.validateSelection()
}

func (b Bundle) sourceIndex() (map[string]SourceSnapshot, error) {
	sources := make(map[string]SourceSnapshot, len(b.Sources))
	for i, source := range b.Sources {
		if err := source.Validate(); err != nil {
			return nil, fmt.Errorf("source %d: %w", i, err)
		}
		id, err := source.ID()
		if err != nil {
			return nil, err
		}
		if source.TMDBID != b.Dossier.TMDBID || id != b.Dossier.SourceIDs[i] {
			return nil, ErrInvalid
		}
		if _, exists := sources[id]; exists {
			return nil, ErrInvalid
		}
		sources[id] = source
	}
	return sources, nil
}

func (p Preparation) validateQuestions(sources map[string]SourceSnapshot) error {
	seen := make(map[string]bool, len(p.Questions))
	for i, q := range p.Questions {
		if err := q.ValidateCandidate(); err != nil {
			return fmt.Errorf("candidate %d: %w", i, err)
		}
		d := q.Measurement
		if d.TMDBID != p.TMDBID || d.DossierID != p.DossierID || seen[d.TargetID] {
			return ErrInvalid
		}
		if d.RegistryVersion == clubprofile.RegistryV2 && d.WatchRevision != p.WatchRevision {
			return ErrInvalid
		}
		seen[d.TargetID] = true
		for _, evidence := range d.Evidence {
			source, exists := sources[evidence.SourceID]
			if !exists || !slices.ContainsFunc(source.Spans, func(s EvidenceSpan) bool { return s.ID == evidence.SpanID }) {
				return ErrInvalid
			}
		}
	}
	return nil
}

func (p Preparation) validateSelection() error {
	s := p.Selection
	if s.PolicyVersion == "" || s.Seed == "" || len(s.SelectedFamilies) != len(p.Questions) || len(s.EligibleFamilies) > 32 {
		return ErrInvalid
	}
	eligible := make(map[string]bool, len(s.EligibleFamilies))
	for _, id := range s.EligibleFamilies {
		if id == "" || eligible[id] {
			return ErrInvalid
		}
		eligible[id] = true
	}
	for i, q := range p.Questions {
		if s.SelectedFamilies[i] != q.Measurement.FamilyID || !eligible[s.SelectedFamilies[i]] {
			return ErrInvalid
		}
	}
	for _, rejected := range s.Rejected {
		if rejected.FamilyID == "" || rejected.Reason == "" || slices.Contains(s.SelectedFamilies, rejected.FamilyID) {
			return ErrInvalid
		}
	}
	return nil
}

package content

import (
	"context"

	"github.com/vaporon4a/movie-helper/internal/ai"
	"github.com/vaporon4a/movie-helper/internal/daily"
	"github.com/vaporon4a/movie-helper/internal/facts"
)

type FactProvider interface {
	Candidates(context.Context, int64) ([]daily.Item, error)
}

// FactSelector reuses provider budgets and durable retry semantics, never the legacy rewrite.
type FactSelector struct {
	Primary, Secondary ai.Generator
	Fallback           *Fallback
}

func (s *FactSelector) SelectEpisodes(ctx context.Context, episodes []facts.Episode) ([]daily.Item, error) {
	var last error
	for n, generator := range []ai.Generator{s.Primary, s.Secondary} {
		if generator == nil {
			continue
		}
		attempt, cancel := context.WithTimeout(ctx, s.Fallback.attemptTimeout(n))
		provider := []string{"gemini", "groq"}[n]
		selected, err := (ai.EpisodeSelector{Generator: generator, Provider: provider, Log: s.Fallback.Log}).SelectEpisodes(attempt, episodes)
		cancel()
		if err == nil {
			return selected, nil
		}
		last = err
		s.Fallback.logFailure(n, err)
		if n == 0 && deferredProviderRetry(err) {
			return nil, err
		}
	}
	return nil, last
}

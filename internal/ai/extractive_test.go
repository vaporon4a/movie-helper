package ai

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/vaporon4a/movie-helper/internal/daily"
	"github.com/vaporon4a/movie-helper/internal/facts"
)

func episodeResult() Selection {
	n, reviewIndex := 0, 0
	return Selection{Index: &n, Reviews: []Review{{Index: &reviewIndex, Reason: "accepted", Interest: 5, Clarity: 4}}}
}
func TestExtractiveSelectionCannotPublishGeneratedText(t *testing.T) {
	episode := facts.Episode{Movie: facts.Movie{ID: 1, Title: "Фильм", Year: 1999}, Text: "Для создания звука команда разбивала арбузы перед микрофоном.", URL: "https://ru.wikipedia.org/w/index.php?oldid=123", Key: "factru:1:hash"}
	for _, bad := range []string{"", "Придуманная история"} {
		t.Run(bad, func(t *testing.T) {
			selector := EpisodeSelector{Provider: "gemini", Generator: generatorFunc(func(_ context.Context, _ string, parts []Part) (Selection, error) {
				if parts[0].FactCandidates != 1 {
					t.Fatal("schema marker lost")
				}
				result := episodeResult()
				result.Text = bad
				return result, nil
			})}
			items, err := selector.SelectEpisodes(t.Context(), []facts.Episode{episode})
			if bad != "" {
				if err == nil {
					t.Fatal("generated text accepted")
				}
				return
			}
			if err != nil || len(items) != 1 || !strings.Contains(items[0].Text, episode.Text) || items[0].GenerationPolicy != daily.FactPolicy {
				t.Fatal(items, err)
			}
			if !strings.Contains(items[0].SourceEvidence, "RevisionID") {
				t.Fatal("provenance missing")
			}
		})
	}
}
func TestExtractiveRejectsMalformedReviews(t *testing.T) {
	for _, mutate := range []func(*Selection){
		func(s *Selection) { *s.Index = 1 }, func(s *Selection) { *s.Index = -1 }, func(s *Selection) { s.Reviews[0].Interest = 3 },
		func(s *Selection) { s.Reviews[0].Clarity = 0 }, func(s *Selection) { s.Reviews[0].Reason = "made_up" }, func(s *Selection) { s.Evidence = "invented" }, func(s *Selection) { s.Reviews = nil },
	} {
		result := episodeResult()
		mutate(&result)
		if validateEpisodeSelection(result, 1) == nil {
			t.Fatal("invalid decision accepted", result)
		}
	}
	result := episodeResult()
	*result.Index = -1
	result.Reviews[0].Reason = "context_required"
	if err := validateEpisodeSelection(result, 1); err != nil {
		t.Fatal("NONE rejected", err)
	}
}

func TestExtractiveResponseJSONRetainsScores(t *testing.T) {
	var result Selection
	err := json.Unmarshal([]byte(`{"index":0,"text":"","evidence":"","reviews":{"0":{"reason":"accepted","interest":5,"clarity":4}}}`), &result)
	if err != nil {
		t.Fatal(err)
	}
	if err = validateEpisodeSelection(result, 1); err != nil {
		t.Fatal("transport contract lost scores", err)
	}
}

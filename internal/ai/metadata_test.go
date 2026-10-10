package ai

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/vaporon4a/movie-helper/internal/daily"
	"github.com/vaporon4a/movie-helper/internal/facts"
)

type namedTestGenerator struct {
	Generator
	model string
}

func (g namedTestGenerator) ModelName() string { return g.model }

func TestExtractiveSelectionRecordsGeneratingModel(t *testing.T) {
	g := namedTestGenerator{model: "generation-model", Generator: generatorFunc(func(context.Context, string, []Part) (Selection, error) {
		return episodeResult(), nil
	})}
	selector := EpisodeSelector{Provider: "groq", Generator: g}
	items, err := selector.SelectEpisodes(t.Context(), []facts.Episode{{Movie: facts.Movie{ID: 1, Title: "Фильм", Year: 2000}, Text: "Текст источника", Key: "key", URL: "https://example.org"}})
	if err != nil || len(items) != 1 || items[0].AIModel != "generation-model" || items[0].AIProvider != "groq" {
		t.Fatal(items, err)
	}
	var evidence episodeEvidence
	if err = json.Unmarshal([]byte(items[0].SourceEvidence), &evidence); err != nil || evidence.Selection.Interest != 5 || evidence.Selection.Clarity != 4 || evidence.Movie.ID != 1 {
		t.Fatal("source or evaluation metadata missing", evidence, err)
	}
}

func TestMemeSelectionRecordsGeneratingModel(t *testing.T) {
	g := namedTestGenerator{model: "meme-model", Generator: generatorFunc(func(context.Context, string, []Part) (Selection, error) {
		n := 0
		return Selection{Index: &n, Reviews: []Review{{Index: &n, Reason: "eligible", Meme: assessment(4, 4, 3)}}}, nil
	})}
	session := (&Editor{Generator: g, Scope: "gemini:meme-model:" + daily.MemePolicy}).memeSession()
	items, _, err := session.selectBatch(t.Context(), []daily.Item{{Kind: daily.Meme, Key: "key", Text: "Подпись", Image: "image"}}, nil, 1, nil)
	if err != nil || len(items) != 1 || items[0].AIModel != "meme-model" || items[0].AIProvider != "gemini" {
		t.Fatal(items, err)
	}
}

func TestUnknownGeneratorModelIsNotGuessed(t *testing.T) {
	if got := generatorModel(generatorFunc(func(context.Context, string, []Part) (Selection, error) { return Selection{}, nil })); got != "" {
		t.Fatal(got)
	}
}

package facts_test

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/vaporon4a/movie-helper/internal/daily"
	"github.com/vaporon4a/movie-helper/internal/facts"
	"github.com/vaporon4a/movie-helper/internal/storage"
)

type catalogSpy struct{ calls int }

func (c *catalogSpy) FactMovies(context.Context, time.Time) ([]facts.Movie, error) {
	c.calls++
	return []facts.Movie{{ID: 1}, {ID: 2}}, nil
}

type sourceSpy struct{ calls int }

func (s *sourceSpy) Episodes(_ context.Context, m facts.Movie) ([]facts.Episode, error) {
	s.calls++
	return []facts.Episode{{Movie: m, Key: facts.EpisodeKey(m.ID, "story"), Text: "story"}}, nil
}

type selectorSpy struct {
	calls int
	batch []facts.Episode
}

func (s *selectorSpy) SelectEpisodes(_ context.Context, e []facts.Episode) ([]daily.Item, error) {
	s.calls++
	s.batch = e
	if s.calls == 1 {
		return nil, errors.New("provider temporarily unavailable")
	}
	return nil, nil
}
func TestPendingSourceBatchSurvivesRestartAndAdvancesOnValidNone(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "facts.db")
	store, err := storage.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.EnsureChat(ctx, -1); err != nil {
		t.Fatal(err)
	}
	catalog, source, selector := &catalogSpy{}, &sourceSpy{}, &selectorSpy{}
	now := func() time.Time { return time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC) }
	pipeline := &facts.Pipeline{Catalog: catalog, Source: source, Selector: selector, Cache: store, History: store, Now: now, Log: slog.New(slog.DiscardHandler)}
	_, err = pipeline.Candidates(ctx, -1)
	if err == nil {
		t.Fatal("failed provider appeared successful")
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = storage.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	pipeline.Cache = store
	pipeline.History = store
	_, err = pipeline.Candidates(ctx, -1)
	if err != nil {
		t.Fatal(err)
	}
	if catalog.calls != 1 || source.calls != 2 || selector.calls != 2 || len(selector.batch) != 2 {
		t.Fatal("retry redownloaded sources", catalog.calls, source.calls, selector.calls)
	}
	_, err = pipeline.Candidates(ctx, -1)
	if err != nil {
		t.Fatal(err)
	}
	if selector.calls != 2 {
		t.Fatal("valid NONE did not advance rotation")
	}
}

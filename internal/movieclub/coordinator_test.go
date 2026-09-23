package movieclub_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/vaporon4a/movie-helper/internal/movieclub"
	"github.com/vaporon4a/movie-helper/internal/storage"
)

type fakeCatalog struct{}

func (fakeCatalog) Discover(_ context.Context, genreID int64, page, _ int) ([]movieclub.Movie, error) {
	movies := make([]movieclub.Movie, 10)
	for i := range movies {
		id := genreID*1000 + int64(page*10+i)
		movies[i] = movieclub.Movie{ID: id, Title: fmt.Sprintf("Фильм %d", id), Overview: "Краткое описание", PosterPath: fmt.Sprintf("/%d.jpg", id), Year: 2024, VoteCount: 500, Rating: 7.5, Popularity: 100 - float64(i)}
	}
	return movies, nil
}

func (fakeCatalog) PosterURL(path string) string { return "https://img.example" + path }

type referenceScenario struct{}

func (referenceScenario) Feature() movieclub.Feature { return movieclub.Reference }
func (referenceScenario) Options(seed uint64) []movieclub.Option {
	options := movieclub.GenreOptions(seed)
	for i := range options {
		options[i].Kind = movieclub.OptionMovie
	}
	return options
}
func (referenceScenario) Winners(options []movieclub.Option, seed uint64) []movieclub.Option {
	return movieclub.Winners(options, seed)
}
func (referenceScenario) Recommendations(context.Context, movieclub.Round, []movieclub.Option, time.Time) ([]movieclub.Recommendation, error) {
	return []movieclub.Recommendation{{Movie: movieclub.Movie{ID: 1, Title: "Похожий фильм", PosterPath: "/1.jpg"}, Page: 1, Relation: "similar"}}, nil
}

type fakeTransport struct {
	opened, closed, summaries int
	pages                     [][]movieclub.Recommendation
	openErr                   error
}

func (f *fakeTransport) OpenPoll(_ context.Context, _ int64, _ movieclub.Feature, labels []string, _ time.Time) (string, int, error) {
	f.opened++
	if f.openErr != nil {
		return "", 0, f.openErr
	}
	if len(labels) != 10 {
		return "", 0, fmt.Errorf("got %d options", len(labels))
	}
	return "poll-1", 101, nil
}

func (f *fakeTransport) ClosePoll(context.Context, int64, int) ([]int, error) {
	f.closed++
	return []int{4, 1, 0, 0, 0, 0, 0, 0, 0, 0}, nil
}

func (f *fakeTransport) SendSummary(context.Context, int64, movieclub.Summary, int64, bool) (int, error) {
	f.summaries++
	return 201, nil
}

func (f *fakeTransport) SendMovies(_ context.Context, _ int64, movies []movieclub.Recommendation) ([]int, error) {
	f.pages = append(f.pages, append([]movieclub.Recommendation(nil), movies...))
	return []int{202}, nil
}

func TestCoordinatorPersistsPollSelectionAndSecondPage(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "movie.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	const chat int64 = -100
	if err = store.EnsureChat(ctx, chat); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	if err = store.SetZone(ctx, 1, chat, "UTC", start); err != nil {
		t.Fatal(err)
	}
	now := start
	transport := &fakeTransport{}
	service, coordinator := movieRuntime(t, store, transport, map[int64]bool{chat: true}, func() time.Time { return now })
	roundID, err := service.Start(ctx, 2, chat, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err = coordinator.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	round, err := store.MovieRound(ctx, chat, roundID)
	if err != nil || round.State != movieclub.StateOpen || transport.opened != 1 {
		t.Fatalf("opened round = %#v, transport=%#v, err=%v", round, transport, err)
	}

	now = start.Add(11 * time.Minute)
	if err = coordinator.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	round, err = store.MovieRound(ctx, chat, roundID)
	if err != nil || round.State != movieclub.StatePublished || round.Page2State != "ready" {
		t.Fatalf("published round = %#v, err=%v", round, err)
	}
	if transport.closed != 1 || transport.summaries != 1 || len(transport.pages) != 1 || len(transport.pages[0]) != 10 {
		t.Fatalf("transport = %#v", transport)
	}
	if err = service.More(ctx, -200, roundID); err == nil {
		t.Fatal("another chat claimed second page")
	}
	if err = service.More(ctx, chat, roundID); err != nil {
		t.Fatal(err)
	}
	if len(transport.pages) != 2 || len(transport.pages[1]) != 10 {
		t.Fatalf("second page = %#v", transport.pages)
	}
	if err = service.More(ctx, chat, roundID); err == nil {
		t.Fatal("second page sent twice")
	}
}

func TestUnknownPollDeliveryRequiresResolution(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "unknown.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	const chat int64 = -101
	if err = store.EnsureChat(ctx, chat); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	transport := &fakeTransport{openErr: &movieclub.DeliveryError{Kind: movieclub.DeliveryUnknown}}
	service, coordinator := movieRuntime(t, store, transport, map[int64]bool{chat: true}, func() time.Time { return now })
	roundID, err := service.Start(ctx, 10, chat, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err = coordinator.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	round, err := store.MovieRound(ctx, chat, roundID)
	if err != nil || round.State != movieclub.StateUnknown || round.ErrorCode != "telegram_unknown" {
		t.Fatalf("unknown round = %#v, %v", round, err)
	}
	if transport.opened != 1 {
		t.Fatalf("poll attempts = %d", transport.opened)
	}
	if err = coordinator.Tick(ctx); err != nil || transport.opened != 1 {
		t.Fatalf("unknown delivery retried: attempts=%d err=%v", transport.opened, err)
	}
}

func TestConcurrentCoordinatorTicksOpenOnePoll(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "concurrent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	const chat int64 = -103
	if err = store.EnsureChat(ctx, chat); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	transport := &fakeTransport{}
	service, coordinator := movieRuntime(t, store, transport, map[int64]bool{chat: true}, func() time.Time { return now })
	if _, err = service.Start(ctx, 30, chat, 10*time.Minute); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	errorsOut := make(chan error, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			errorsOut <- coordinator.Tick(ctx)
		}()
	}
	close(start)
	workers.Wait()
	close(errorsOut)
	for tickErr := range errorsOut {
		if tickErr != nil {
			t.Fatal(tickErr)
		}
	}
	if transport.opened != 1 {
		t.Fatalf("concurrent ticks opened %d polls", transport.opened)
	}
}

func movieRuntime(t *testing.T, store *storage.Store, transport *fakeTransport, allowed map[int64]bool, now func() time.Time) (*movieclub.Service, *movieclub.Coordinator) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	scenario, err := movieclub.NewGenreScenario(fakeCatalog{}, store)
	if err != nil {
		t.Fatal(err)
	}
	scenarios, err := movieclub.NewScenarioSet(scenario)
	if err != nil {
		t.Fatal(err)
	}
	service, err := movieclub.NewService(store, transport, scenario, log, now)
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err := movieclub.NewCoordinator(store, transport, scenarios, allowed, log, now)
	if err != nil {
		t.Fatal(err)
	}
	return service, coordinator
}

func TestGenreRotationTieAndDSTSlot(t *testing.T) {
	one := movieclub.GenreOptions(42)
	two := movieclub.GenreOptions(42)
	if len(one) != 10 || fmt.Sprint(one) != fmt.Sprint(two) {
		t.Fatalf("rotation is not deterministic: %#v %#v", one, two)
	}
	seen := make(map[int64]bool)
	for i := range one {
		if seen[one[i].ProviderID] || one[i].Position != i {
			t.Fatalf("invalid option %#v", one[i])
		}
		seen[one[i].ProviderID] = true
		one[i].Votes = 3
	}
	if winners := movieclub.Winners(one, 7); len(winners) != 2 {
		t.Fatalf("tie winners = %#v", winners)
	}

	now := time.Date(2026, 3, 29, 0, 0, 0, 0, time.UTC)
	slot, err := movieclub.WeeklySlot(now, int(time.Sunday), "02:30", "Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	local := slot.In(mustLocation(t, "Europe/Berlin"))
	if local.Day() != 29 || local.Hour() != 3 || local.Minute() != 0 {
		t.Fatalf("DST gap slot = %s", local)
	}
}

func TestScenarioContractRunsReferenceRoundThroughSharedLifecycle(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "reference.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	const chat int64 = -102
	if err = store.EnsureChat(ctx, chat); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	transport := &fakeTransport{}
	scenario := referenceScenario{}
	scenarios, err := movieclub.NewScenarioSet(scenario)
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	service, err := movieclub.NewService(store, transport, scenario, log, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err := movieclub.NewCoordinator(store, transport, scenarios, map[int64]bool{chat: true}, log, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	roundID, err := service.Start(ctx, 20, chat, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err = coordinator.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	now = now.Add(6 * time.Minute)
	if err = coordinator.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	round, err := store.MovieRound(ctx, chat, roundID)
	if err != nil || round.Feature != movieclub.Reference || round.State != movieclub.StatePublished {
		t.Fatalf("reference round = %#v, err=%v", round, err)
	}
}

func mustLocation(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

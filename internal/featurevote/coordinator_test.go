package featurevote_test

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/vaporon4a/movie-helper/internal/featurevote"
	"github.com/vaporon4a/movie-helper/internal/storage"
)

type titleStub struct{}

func (titleStub) Title(context.Context, string) string {
	return "Короткое название новой функции"
}

type featureSenderStub struct {
	opens, closes, results int
}

func (s *featureSenderStub) OpenRound(_ context.Context, round featurevote.Round, payload string) (featurevote.OpenResult, error) {
	s.opens++
	if len(round.Options) == 0 || payload == "" {
		return featurevote.OpenResult{}, featurevote.ErrConflict
	}
	return featurevote.OpenResult{MessageID: 77, PollID: "native-poll", Mode: featurevote.BallotNative}, nil
}

func (s *featureSenderStub) CloseRound(_ context.Context, round featurevote.Round) ([]int, error) {
	s.closes++
	if round.PollID == "" || len(round.Options) != 2 {
		return nil, featurevote.ErrConflict
	}
	return []int{1, 0}, nil
}

func (s *featureSenderStub) SendResult(_ context.Context, round featurevote.Round) error {
	s.results++
	if round.Outcome == "" {
		return featurevote.ErrConflict
	}
	return nil
}

func TestCoordinatorOpensAllActiveIdeasAndPublishesWinner(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "feature.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.EnsureChat(ctx, -1); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC)
	if err = store.SetZone(ctx, 1, -1, "UTC", now); err != nil {
		t.Fatal(err)
	}
	service, err := featurevote.NewService(store, slog.New(slog.NewTextHandler(io.Discard, nil)), func() time.Time { return now }, func() (string, error) { return "main-token", nil })
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.Add(ctx, 2, -1, 101, "Добавить общий список просмотренных фильмов")
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Add(ctx, 3, -1, 102, "Добавить напоминания перед началом киновечера")
	if err != nil {
		t.Fatal(err)
	}
	roundID, err := service.Start(ctx, 4, -1, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	sender := &featureSenderStub{}
	coordinator, err := featurevote.NewCoordinator(store, sender, titleStub{}, map[int64]bool{-1: true}, slog.New(slog.NewTextHandler(io.Discard, nil)), func() time.Time { return now }, func() (string, error) { return "runoff-token", nil })
	if err != nil {
		t.Fatal(err)
	}
	if err = coordinator.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	round, err := store.FeatureRound(ctx, roundID)
	if err != nil || round.State != featurevote.RoundOpen {
		t.Fatalf("round=%#v err=%v", round, err)
	}
	options, err := store.FeatureRoundOptions(ctx, roundID)
	if err != nil || len(options) != 2 || options[0].IdeaID != first.ID || options[1].IdeaID != second.ID {
		t.Fatalf("options=%#v err=%v", options, err)
	}
	now = now.Add(6 * time.Minute)
	if err = coordinator.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	round, err = store.FeatureRound(ctx, roundID)
	if err != nil || round.State != featurevote.RoundPublished || round.WinnerID != first.ID {
		t.Fatalf("round=%#v err=%v", round, err)
	}
	if sender.opens != 1 || sender.closes != 1 || sender.results != 1 {
		t.Fatalf("sender opens=%d closes=%d results=%d", sender.opens, sender.closes, sender.results)
	}
	backlog, err := service.Backlog(ctx, -1)
	if err != nil || len(backlog) != 1 || backlog[0].ID != first.ID {
		t.Fatalf("backlog=%#v err=%v", backlog, err)
	}
}

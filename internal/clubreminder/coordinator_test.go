package clubreminder_test

import (
	"context"
	"github.com/vaporon4a/movie-helper/internal/clubreminder"
	"github.com/vaporon4a/movie-helper/internal/daily"
	"github.com/vaporon4a/movie-helper/internal/storage"
	"log/slog"
	"path/filepath"
	"testing"
	"time"
)

type sender struct {
	count  int
	err    error
	text   string
	onSend func()
}

func (s *sender) SendReminder(_ context.Context, _ int64, text string) (int, error) {
	s.count++
	if s.onSend != nil {
		s.onSend()
	}
	s.text = text
	return 1, s.err
}
func TestScheduledDeliveryFailuresAndRestart(t *testing.T) {
	for _, tc := range []struct {
		name, kind string
		calls      int
	}{{"success", "", 1}, {"429", "retry", 2}, {"forbidden", "forbidden", 1}, {"timeout", "unknown", 1}} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			now := time.Date(2026, 10, 19, 16, 0, 0, 0, time.UTC)
			if err = store.EnsureChat(ctx, -1); err != nil {
				t.Fatal(err)
			}
			if err = store.SetZone(ctx, 1, -1, "Europe/Moscow", now.Add(-time.Hour)); err != nil {
				t.Fatal(err)
			}
			if err = store.SetClubSchedule(ctx, 2, -1, 5, 2, "19:00", now.Add(-time.Hour)); err != nil {
				t.Fatal(err)
			}
			transport := &sender{}
			if tc.kind != "" {
				transport.err = &daily.SendError{Kind: tc.kind, After: time.Minute}
			}
			worker := clubreminder.Coordinator{Store: store, Sender: transport, Allowed: map[int64]bool{-1: true}, Log: slog.New(slog.DiscardHandler), Now: func() time.Time { return now }}
			if err = worker.Tick(ctx); err != nil {
				t.Fatal(err)
			}
			first := transport.text
			transport.err = nil
			now = now.Add(2 * time.Minute)
			if err = store.RecoverClub(ctx); err != nil {
				t.Fatal(err)
			}
			if err = worker.Tick(ctx); err != nil {
				t.Fatal(err)
			}
			if transport.count != tc.calls {
				t.Fatalf("calls=%d expected=%d", transport.count, tc.calls)
			}
			if transport.text != first {
				t.Fatal("retry changed text")
			}
		})
	}
}
func TestNoCatchupOutsideWindow(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "late.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, 10, 20, 16, 0, 0, 0, time.UTC)
	if err = store.EnsureChat(ctx, -1); err != nil {
		t.Fatal(err)
	}
	if err = store.SetZone(ctx, 1, -1, "Europe/Moscow", now.Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err = store.SetClubSchedule(ctx, 2, -1, 5, 2, "19:00", now.Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	transport := &sender{}
	worker := clubreminder.Coordinator{Store: store, Sender: transport, Allowed: map[int64]bool{-1: true}, Log: slog.Default(), Now: func() time.Time { return now }}
	if err = worker.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if transport.count != 0 {
		t.Fatal("stale reminder sent")
	}
}

func TestTelegramRetryAfterStartsAfterSend(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "retry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, 10, 19, 16, 0, 0, 0, time.UTC)
	if err = store.EnsureChat(ctx, -1); err != nil {
		t.Fatal(err)
	}
	if err = store.SetZone(ctx, 1, -1, "Europe/Moscow", now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err = store.SetClubSchedule(ctx, 2, -1, 5, 2, "19:00", now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	transport := &sender{err: &daily.SendError{Kind: "retry", After: time.Minute}, onSend: func() { now = now.Add(10 * time.Second) }}
	c := clubreminder.Coordinator{Store: store, Sender: transport, Allowed: map[int64]bool{-1: true}, Log: slog.Default(), Now: func() time.Time { return now }}
	if err = c.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	due, err := store.ClubDue(ctx, now.Add(50*time.Second))
	if err != nil || len(due) != 0 {
		t.Fatal("retry too early", due, err)
	}
	due, err = store.ClubDue(ctx, now.Add(time.Minute))
	if err != nil || len(due) != 1 {
		t.Fatal("retry not scheduled", due, err)
	}
}

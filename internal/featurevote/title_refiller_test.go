package featurevote_test

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/vaporon4a/movie-helper/internal/aiwork"
	"github.com/vaporon4a/movie-helper/internal/featurevote"
	"github.com/vaporon4a/movie-helper/internal/storage"
)

type backgroundTitleStub struct{ ok bool }

func (s backgroundTitleStub) TryTitle(context.Context, string) (string, bool) {
	return "Умные напоминания перед киновечером", s.ok
}

func TestIdeaTitleIsPreparedOutsidePollOpening(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "title.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.EnsureChat(ctx, -1); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	service, err := featurevote.NewService(store, slog.New(slog.NewTextHandler(io.Discard, nil)), func() time.Time { return now }, func() (string, error) { return "token", nil })
	if err != nil {
		t.Fatal(err)
	}
	idea, err := service.Add(ctx, 1, -1, 42, "Добавить умные напоминания перед началом следующего киновечера")
	if err != nil {
		t.Fatal(err)
	}
	work, claimed, err := store.ClaimAIWork(ctx, now, time.Minute)
	if err != nil || !claimed || work.Kind != aiwork.FeatureTitle || work.ScopeID != idea.ID {
		t.Fatal(work, claimed, err)
	}
	refiller := featurevote.TitleRefiller{Store: store, Generator: backgroundTitleStub{ok: true}}
	outcome, err := refiller.Execute(ctx, work, now)
	if err != nil || !outcome.Done {
		t.Fatal(outcome, err)
	}
	prepared, err := store.FeatureForTitle(ctx, idea.ID)
	if err != nil || prepared.Title != "Умные напоминания перед киновечером" {
		t.Fatal(prepared, err)
	}
}

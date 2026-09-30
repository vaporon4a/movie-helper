package daily_test

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/vaporon4a/movie-helper/internal/aiwork"
	"github.com/vaporon4a/movie-helper/internal/daily"
	"github.com/vaporon4a/movie-helper/internal/storage"
)

type foregroundProviderSpy struct{ calls int }

func (p *foregroundProviderSpy) Candidates(context.Context, string, int64) ([]daily.Item, error) {
	p.calls++
	return []daily.Item{{Kind: daily.Fact, Key: "unexpected"}}, nil
}

func TestBackgroundPreviewNeverCallsForegroundProvider(t *testing.T) {
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "daily.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.EnsureChat(ctx, -1); err != nil {
		t.Fatal(err)
	}
	provider := &foregroundProviderSpy{}
	service, err := daily.NewService(store, provider, slog.New(slog.NewTextHandler(io.Discard, nil)), true)
	if err != nil {
		t.Fatal(err)
	}
	items, err := service.Candidates(ctx, daily.Fact, -1)
	if err != nil || len(items) != 0 || provider.calls != 0 {
		t.Fatal(items, err, provider.calls)
	}
	work, claimed, err := store.ClaimAIWork(ctx, time.Now(), time.Minute)
	if err != nil || !claimed || work.Kind != aiwork.FactRefill || work.ScopeID != -1 {
		t.Fatal(work, claimed, err)
	}
}

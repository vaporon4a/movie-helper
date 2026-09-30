package content

import (
	"context"
	"testing"
	"time"

	"github.com/vaporon4a/movie-helper/internal/aiwork"
	"github.com/vaporon4a/movie-helper/internal/daily"
)

type refillStore struct {
	stock, saved int
}

func (s *refillStore) AIStock(context.Context, int64, string, time.Time) (int, error) {
	return s.stock, nil
}
func (s *refillStore) SaveAIItems(_ context.Context, _ int64, _ string, items []daily.Item, _ time.Time) (int, error) {
	s.saved += len(items)
	s.stock += len(items)
	return len(items), nil
}

type refillProvider struct {
	calls int
	err   error
}

func (p *refillProvider) Candidates(_ context.Context, kind string, _ int64) ([]daily.Item, error) {
	p.calls++
	if p.err != nil {
		return nil, p.err
	}
	return []daily.Item{
		{Kind: kind, Image: "https://i.redd.it/one.jpg", Key: "one"},
		{Kind: kind, Image: "https://i.redd.it/two.jpg", Key: "two"},
		{Kind: kind, Image: "https://i.redd.it/three.jpg", Key: "three"},
	}, nil
}

func TestRefillerDefersDailyLimitUntilNextUTCWindow(t *testing.T) {
	now := time.Date(2026, 9, 30, 23, 58, 0, 0, time.UTC)
	provider := &refillProvider{err: &daily.PreviewError{Code: "local_daily_limit"}}
	refiller := &Refiller{Store: &refillStore{}, Provider: provider}
	outcome, err := refiller.Execute(context.Background(), aiwork.Work{Kind: aiwork.FactRefill, ScopeID: -1}, now)
	if err != nil || outcome.Done || outcome.Reason != "local_daily_limit" || outcome.After != 7*time.Minute {
		t.Fatal(outcome, err)
	}
}

func TestRefillerStopsAtTarget(t *testing.T) {
	store := &refillStore{stock: 4}
	provider := &refillProvider{}
	refiller := &Refiller{Store: store, Provider: provider}
	outcome, err := refiller.Execute(context.Background(), aiwork.Work{Kind: aiwork.MemeRefill, ScopeID: -1}, time.Now())
	if err != nil || !outcome.Done || store.stock != 5 || store.saved != 1 || provider.calls != 1 {
		t.Fatal(outcome, err, store, provider.calls)
	}
	outcome, err = refiller.Execute(context.Background(), aiwork.Work{Kind: aiwork.MemeRefill, ScopeID: -1}, time.Now())
	if err != nil || !outcome.Done || provider.calls != 1 {
		t.Fatal(outcome, err, provider.calls)
	}
}

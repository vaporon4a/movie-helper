package aiwork

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

type repoStub struct {
	work              Work
	claimed           bool
	seeded, completed int
	deferred          string
	next              time.Time
}

func (r *repoStub) SeedAIWork(context.Context, time.Time) error { r.seeded++; return nil }
func (r *repoStub) ClaimAIWork(context.Context, time.Time, time.Duration) (Work, bool, error) {
	if r.claimed {
		return Work{}, false, nil
	}
	r.claimed = true
	return r.work, true, nil
}
func (r *repoStub) DeferAIWork(_ context.Context, _ int64, next time.Time, reason string) error {
	r.deferred, r.next = reason, next
	return nil
}
func (r *repoStub) CompleteAIWork(context.Context, int64, time.Time) error { r.completed++; return nil }

type executorStub struct{ outcome Outcome }

func (e executorStub) Execute(context.Context, Work, time.Time) (Outcome, error) {
	return e.outcome, nil
}

func TestCoordinatorCompletesAndDefersDurableWork(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name    string
		outcome Outcome
		done    int
		reason  string
	}{
		{"complete", Outcome{Done: true}, 1, ""},
		{"defer", Outcome{After: 5 * time.Minute, Reason: "provider_unavailable"}, 0, "provider_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &repoStub{work: Work{ID: 1, Kind: FactRefill, ScopeID: -1, Attempts: 1}}
			coordinator, err := NewCoordinator(repo, map[string]Executor{FactRefill: executorStub{outcome: tc.outcome}}, slog.New(slog.NewTextHandler(io.Discard, nil)), func() time.Time { return now })
			if err != nil {
				t.Fatal(err)
			}
			if err = coordinator.Tick(context.Background()); err != nil {
				t.Fatal(err)
			}
			if repo.seeded != 1 || repo.completed != tc.done || repo.deferred != tc.reason {
				t.Fatal(repo)
			}
			if tc.reason != "" && !repo.next.Equal(now.Add(5*time.Minute)) {
				t.Fatal(repo.next)
			}
		})
	}
}

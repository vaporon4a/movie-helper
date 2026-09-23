package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vaporon4a/movie-helper/internal/daily"
)

type flakyProvider struct {
	calls, failures int
	empty           bool
}

func (p *flakyProvider) Candidates(_ context.Context, kind string, _ int64) ([]daily.Item, error) {
	p.calls++
	if p.calls <= p.failures {
		if p.empty {
			return nil, nil
		}
		return nil, errors.New("upstream 503")
	}
	return []daily.Item{{Kind: kind, Text: "Материал", Image: "https://i.redd.it/test.jpg", Source: "https://example.org/source", Key: "retry:" + kind}}, nil
}

func TestPreparationRetriesBothKindsAndSurvivesRestart(t *testing.T) {
	for _, kind := range []string{daily.Meme, daily.Fact} {
		for _, empty := range []bool{false, true} {
			t.Run(kind+map[bool]string{true: "empty", false: "error"}[empty], func(t *testing.T) {
				s, send, n := fixture(t)
				ctx := context.Background()
				if kind == daily.Fact {
					if err := storeOf(s).SetSchedule(ctx, 3, -1, daily.Meme, "09:00", false, *n); err != nil {
						t.Fatal(err)
					}
					if err := storeOf(s).SetSchedule(ctx, 4, -1, daily.Fact, "09:00", true, *n); err != nil {
						t.Fatal(err)
					}
				}
				p := &flakyProvider{failures: 1, empty: empty}
				s.Provider = p
				*n = n.Add(time.Hour)
				tick(t, s)
				tick(t, s)
				if p.calls != 1 || send.calls != 0 {
					t.Fatal("retried immediately", p.calls, send.calls)
				}
				rows, err := storeOf(s).Preparing(ctx)
				if err != nil || len(rows) != 1 || rows[0].FetchAttempts != 1 || rows[0].NextAttempt != n.Add(5*time.Minute).Unix() {
					t.Fatal(rows, err)
				}
				// Startup recovery and a fresh scheduler preserve the retry deadline.
				if err := storeOf(s).Recover(ctx); err != nil {
					t.Fatal(err)
				}
				fresh := &Scheduler{Store: s.Store, Sender: send, Provider: p, Allowed: s.Allowed, Log: s.Log, Now: s.Now}
				*n = n.Add(4 * time.Minute)
				tick(t, fresh)
				if p.calls != 1 {
					t.Fatal("lost persisted delay")
				}
				*n = n.Add(time.Minute)
				tick(t, fresh)
				tick(t, fresh)
				if p.calls != 2 || send.calls != 1 {
					t.Fatal("retry did not deliver once", p.calls, send.calls)
				}
			})
		}
	}
}

func TestPreparationBoundedAndCancelled(t *testing.T) {
	s, send, n := fixture(t)
	p := &flakyProvider{failures: 100}
	s.Provider = p
	*n = n.Add(time.Hour)
	tick(t, s)
	for attempt := 1; attempt < daily.MaxPreparationAttempts; attempt++ {
		*n = n.Add(preparationDelay(attempt))
		tick(t, s)
	}
	*n = n.Add(time.Minute)
	tick(t, s)
	rows, err := storeOf(s).Preparing(context.Background())
	if err != nil || len(rows) != 0 || p.calls != 6 || send.calls != 0 {
		t.Fatal(rows, err, p.calls, send.calls)
	}
	s, send, n = fixture(t)
	p = &flakyProvider{failures: 1}
	s.Provider = p
	*n = n.Add(time.Hour)
	tick(t, s)
	if err := storeOf(s).SetSchedule(context.Background(), 3, -1, daily.Meme, "09:00", false, *n); err != nil {
		t.Fatal(err)
	}
	*n = n.Add(10 * time.Minute)
	tick(t, s)
	if p.calls != 1 || send.calls != 0 {
		t.Fatal("pause did not cancel retry")
	}
}

func TestPreparationWindowAndLateResult(t *testing.T) {
	zone, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	slot := time.Date(2026, 9, 22, 23, 50, 0, 0, zone)
	if got := preparationDeadline(slot); got.Sub(slot) != 10*time.Minute {
		t.Fatal(got)
	}
	s, send, n := fixture(t)
	*n = n.Add(3 * time.Hour)
	tick(t, s)
	if send.calls != 1 {
		t.Fatal("missed slot not recovered in six-hour window")
	}
	s, send, n = fixture(t)
	s.Provider.(*provider).hook = func() { *n = n.Add(7 * time.Hour) }
	*n = n.Add(time.Hour)
	tick(t, s)
	if send.calls != 0 {
		t.Fatal("result published past deadline")
	}
}

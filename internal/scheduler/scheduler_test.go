package scheduler

import (
	"context"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/vaporon4a/movie-helper/internal/daily"
	"github.com/vaporon4a/movie-helper/internal/storage"
)

func TestSlotCalendar(t *testing.T) {
	cases := []struct{ now, clock, zone, want string }{
		{"2026-09-21T21:05:00Z", "00:00", "Europe/Moscow", "2026-09-21T21:00:00Z"},
		{"2026-09-22T13:05:00Z", "09:00", "America/New_York", "2026-09-22T13:00:00Z"},
		{"2026-03-29T02:05:00Z", "02:30", "Europe/Berlin", "2026-03-29T01:00:00Z"},
		{"2026-10-25T01:45:00Z", "02:30", "Europe/Berlin", "2026-10-25T00:30:00Z"},
	}
	for _, c := range cases {
		t.Run(c.zone+c.now, func(t *testing.T) {
			n, _ := time.Parse(time.RFC3339, c.now)
			got, e := Slot(n, c.clock, c.zone)
			if e != nil || got.UTC().Format(time.RFC3339) != c.want {
				t.Fatalf("%v %v", got, e)
			}
		})
	}
}

type sender struct {
	calls int
	err   error
	hook  func()
}

func (s *sender) Send(context.Context, int64, daily.Item) (int, error) {
	s.calls++
	if s.hook != nil {
		s.hook()
	}
	return 123, s.err
}

type provider struct {
	calls int
	hook  func()
}

func storeOf(t *testing.T, s *Scheduler) *storage.Store {
	t.Helper()
	store, ok := s.deliveries.(*storage.Store)
	if !ok {
		t.Fatalf("unexpected storage fixture: %T", s.deliveries)
	}
	return store
}

func (p *provider) Candidates(_ context.Context, kind string, _ int64) ([]daily.Item, error) {
	p.calls++
	if p.hook != nil {
		p.hook()
	}
	if kind != daily.Meme {
		return nil, nil
	}
	return []daily.Item{{Kind: daily.Meme, Text: "Мем", Image: "https://i.redd.it/test.jpg", Source: "https://redd.it/test", Key: "reddit:test"}}, nil
}

func TestModerationWaitsForApproval(t *testing.T) {
	s, send, n := fixture(t)
	ctx := context.Background()
	if err := storeOf(t, s).SetModeration(ctx, 10, -1, true, *n); err != nil {
		t.Fatal(err)
	}
	*n = n.Add(time.Hour)
	tick(t, s)
	q, err := storeOf(t, s).Queue(ctx, -1, 0)
	if err != nil || len(q) != 1 || q[0].State != "pending" || send.calls != 0 {
		t.Fatal(q, err, send.calls)
	}
	if err = storeOf(t, s).Moderate(ctx, 11, -1, q[0].ID, 42, true, *n); err != nil {
		t.Fatal(err)
	}
	tick(t, s)
	if send.calls != 0 {
		t.Fatal("approval sent outside next scheduled slot")
	}
	*n = n.Add(24 * time.Hour)
	tick(t, s)
	if send.calls != 1 || providerOf(t, s).calls != 1 {
		t.Fatal("approved queue was not used")
	}
}

func TestAutomaticModeIgnoresHumanQueue(t *testing.T) {
	s, send, n := fixture(t)
	ctx := context.Background()
	id, err := storeOf(t, s).Add(ctx, 10, daily.Item{ChatID: -1, AuthorID: 42, Kind: daily.Meme, Image: "file-id", Key: "manual"}, *n)
	if err != nil {
		t.Fatal(err)
	}
	if err = storeOf(t, s).Moderate(ctx, 11, -1, id, 42, true, *n); err != nil {
		t.Fatal(err)
	}
	*n = n.Add(time.Hour)
	tick(t, s)
	item, err := storeOf(t, s).Item(ctx, -1, id)
	if err != nil || item.State != "approved" || send.calls != 1 || providerOf(t, s).calls != 1 {
		t.Fatal(item, err, send.calls)
	}
}

func TestModeChangeDuringFetchCancelsSlot(t *testing.T) {
	s, send, n := fixture(t)
	providerOf(t, s).hook = func() {
		if err := storeOf(t, s).SetModeration(context.Background(), 10, -1, true, *n); err != nil {
			t.Fatal(err)
		}
	}
	*n = n.Add(time.Hour)
	tick(t, s)
	tick(t, s)
	if send.calls != 0 {
		t.Fatal("published after moderation enabled")
	}
}

func TestModeChangeCancelsRetryAndRequiresHumanApproval(t *testing.T) {
	s, send, n := fixture(t)
	ctx := context.Background()
	*n = n.Add(time.Hour)
	send.err = &daily.SendError{Kind: "retry", After: 30 * time.Second}
	tick(t, s)
	if err := storeOf(t, s).SetModeration(ctx, 10, -1, true, *n); err != nil {
		t.Fatal(err)
	}
	q, err := storeOf(t, s).Queue(ctx, -1, 0)
	if err != nil || len(q) != 1 || q[0].State != "pending" {
		t.Fatal(q, err)
	}
	*n = n.Add(30 * time.Second)
	tick(t, s)
	*n = n.Add(24 * time.Hour)
	tick(t, s)
	if send.calls != 1 {
		t.Fatal("AI approval bypassed human moderation")
	}
	if err = storeOf(t, s).SetModeration(ctx, 11, -1, false, *n); err != nil {
		t.Fatal(err)
	}
	send.err = nil
	*n = n.Add(24 * time.Hour)
	tick(t, s)
	if send.calls != 2 {
		t.Fatal("AI approved item lost after switching back")
	}
}
func fixture(t *testing.T) (*Scheduler, *sender, *time.Time) {
	t.Helper()
	ctx := context.Background()
	st, e := storage.Open(ctx, filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { st.Close() })
	n := time.Date(2026, 9, 22, 8, 0, 0, 0, time.UTC)
	for _, e := range []error{st.EnsureChat(ctx, -1), st.SetZone(ctx, 1, -1, "UTC", n), st.SetSchedule(ctx, 2, -1, "meme", "09:00", true, n)} {
		if e != nil {
			t.Fatal(e)
		}
	}
	send := &sender{}
	s := &Scheduler{schedules: st, preparation: st, stock: st, deliveries: st, Sender: send, Provider: &provider{}, Allowed: map[int64]bool{-1: true}, Log: slog.New(slog.DiscardHandler), Now: func() time.Time { return n }}
	return s, send, &n
}
func tick(t *testing.T, s *Scheduler) {
	t.Helper()
	if e := s.Tick(context.Background()); e != nil {
		t.Fatal(e)
	}
}
func TestSendOnceAndNoRepeatNextDay(t *testing.T) {
	s, send, n := fixture(t)
	*n = n.Add(time.Hour)
	tick(t, s)
	tick(t, s)
	if send.calls != 1 {
		t.Fatal(send.calls)
	}
	if e := storeOf(t, s).Recover(context.Background()); e != nil {
		t.Fatal(e)
	}
	tick(t, s)
	if send.calls != 1 {
		t.Fatal("restart duplicate")
	}
	*n = n.Add(24 * time.Hour)
	tick(t, s)
	if send.calls != 1 {
		t.Fatal("same meme reused")
	}
}

func TestConcurrentTicksPublishOnce(t *testing.T) {
	s, send, now := fixture(t)
	*now = now.Add(time.Hour)
	start := make(chan struct{})
	errorsOut := make(chan error, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Go(func() {
			<-start
			errorsOut <- s.Tick(context.Background())
		})
	}
	close(start)
	workers.Wait()
	close(errorsOut)
	for err := range errorsOut {
		if err != nil {
			t.Fatal(err)
		}
	}
	if send.calls != 1 {
		t.Fatalf("concurrent ticks sent %d messages", send.calls)
	}
}
func TestRetryAfterAndExpiry(t *testing.T) {
	s, send, n := fixture(t)
	*n = n.Add(time.Hour)
	send.err = &daily.SendError{Kind: "retry", After: 30 * time.Second}
	tick(t, s)
	tick(t, s)
	if send.calls != 1 {
		t.Fatal("ignored retry_after")
	}
	*n = n.Add(30 * time.Second)
	send.err = nil
	tick(t, s)
	if send.calls != 2 {
		t.Fatal(send.calls)
	}
	s, send, n = fixture(t)
	*n = n.Add(time.Hour)
	send.err = &daily.SendError{Kind: "retry", After: 7 * time.Hour}
	tick(t, s)
	*n = n.Add(7 * time.Hour)
	tick(t, s)
	if send.calls != 1 {
		t.Fatal("expired retry")
	}
}
func TestUnknownNeverRetried(t *testing.T) {
	s, send, n := fixture(t)
	*n = n.Add(time.Hour)
	send.err = &daily.SendError{Kind: "unknown"}
	tick(t, s)
	if e := storeOf(t, s).Recover(context.Background()); e != nil {
		t.Fatal(e)
	}
	tick(t, s)
	if send.calls != 1 {
		t.Fatal(send.calls)
	}
	issues, e := storeOf(t, s).Issues(context.Background(), -1)
	if e != nil || len(issues) != 1 || issues[0].State != "unknown" {
		t.Fatal(issues, e)
	}
}
func TestMissedWindowAndActivation(t *testing.T) {
	s, send, n := fixture(t)
	*n = n.Add(7 * time.Hour)
	tick(t, s)
	if send.calls != 0 {
		t.Fatal("old slot caught up")
	}
	*n = n.Add(-6 * time.Hour)
	if e := storeOf(t, s).SetSchedule(context.Background(), 3, -1, "meme", "09:00", true, *n); e != nil {
		t.Fatal(e)
	}
	tick(t, s)
	if send.calls != 0 {
		t.Fatal("activation sent immediately")
	}
}
func TestForbiddenSuspendsAndPermanentDoesNot(t *testing.T) {
	for _, kind := range []string{"forbidden", "permanent"} {
		t.Run(kind, func(t *testing.T) {
			s, send, n := fixture(t)
			*n = n.Add(time.Hour)
			send.err = &daily.SendError{Kind: kind}
			tick(t, s)
			tick(t, s)
			if send.calls != 1 {
				t.Fatal(send.calls)
			}
			sc, e := storeOf(t, s).Schedules(context.Background(), -1)
			if e != nil {
				t.Fatal(e)
			}
			for _, x := range sc {
				if x.Kind == "meme" && x.Enabled != (kind != "forbidden") {
					t.Fatal(x)
				}
			}
		})
	}
}
func TestEmptyFactQueueAndDisallowedChat(t *testing.T) {
	s, send, n := fixture(t)
	ctx := context.Background()
	if e := storeOf(t, s).SetSchedule(ctx, 3, -1, "meme", "09:00", false, *n); e != nil {
		t.Fatal(e)
	}
	if e := storeOf(t, s).SetSchedule(ctx, 4, -1, "fact", "09:00", true, *n); e != nil {
		t.Fatal(e)
	}
	*n = n.Add(time.Hour)
	tick(t, s)
	if send.calls != 0 {
		t.Fatal("empty queue sent")
	}
	s, send, n = fixture(t)
	s.Allowed = map[int64]bool{}
	*n = n.Add(time.Hour)
	tick(t, s)
	if send.calls != 0 {
		t.Fatal("disallowed chat sent")
	}
}

func providerOf(t *testing.T, s *Scheduler) *provider {
	t.Helper()
	p, ok := s.Provider.(*provider)
	if !ok {
		t.Fatalf("unexpected provider fixture: %T", s.Provider)
	}
	return p
}

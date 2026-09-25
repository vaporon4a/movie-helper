package storage

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/vaporon4a/movie-helper/internal/daily"
)

var testNow = time.Date(2026, 9, 22, 8, 0, 0, 0, time.UTC)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, e := Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func must(t *testing.T, e error) {
	t.Helper()
	if e != nil {
		t.Fatal(e)
	}
}
func fact(chat int64, key string) daily.Item {
	return daily.Item{ChatID: chat, AuthorID: 42, Kind: daily.Fact, Text: "Факт", Source: "https://example.org/interview", Key: key}
}
func setup(t *testing.T, s *Store, chat int64) {
	t.Helper()
	ctx := context.Background()
	must(t, s.EnsureChat(ctx, chat))
	must(t, s.SetZone(ctx, chat*10, chat, "UTC", testNow))
	must(t, s.SetModeration(ctx, chat*10-2, chat, true, testNow))
	must(t, s.SetSchedule(ctx, chat*10-1, chat, "fact", "09:00", true, testNow))
}
func reserve(t *testing.T, s *Store) int64 {
	t.Helper()
	ctx := context.Background()
	sc, e := s.Schedules(ctx, -1)
	must(t, e)
	for _, x := range sc {
		if x.Kind == daily.Fact {
			id, e := s.Reserve(ctx, x, "2026-09-22", testNow.Add(time.Hour).Unix(), testNow.Add(2*time.Hour).Unix())
			must(t, e)
			return id
		}
	}
	t.Fatal("no schedule")
	return 0
}
func TestMigrationsAndPersistence(t *testing.T) {
	ctx := context.Background()
	p := filepath.Join(t.TempDir(), "test.db")
	s, e := Open(ctx, p)
	must(t, e)
	setup(t, s, -1)
	id, e := s.Add(ctx, 1, fact(-1, "fact:one"), testNow)
	must(t, e)
	must(t, s.Close())
	s, e = Open(ctx, p)
	must(t, e)
	defer s.Close()
	i, e := s.Item(ctx, -1, id)
	must(t, e)
	if i.Text != "Факт" {
		t.Fatal(i)
	}
	var fk int
	must(t, s.db.QueryRow("PRAGMA foreign_keys").Scan(&fk))
	if fk != 1 {
		t.Fatal("foreign keys disabled")
	}
	pvd, e := s.migrator()
	must(t, e)
	_, e = pvd.DownTo(ctx, 0)
	must(t, e)
	_, e = pvd.Up(ctx)
	must(t, e)
	must(t, s.EnsureChat(ctx, -1))
	items, e := s.Queue(ctx, -1, 0)
	must(t, e)
	if len(items) != 0 {
		t.Fatal("Down did not remove data")
	}
}
func TestIsolationDedupAndModeration(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	setup(t, s, -1)
	setup(t, s, -2)
	id, e := s.Add(ctx, 1, fact(-1, "one"), testNow)
	must(t, e)
	_, e = s.Add(ctx, 1, fact(-1, "two"), testNow)
	if !errors.Is(e, daily.ErrDuplicate) {
		t.Fatal(e)
	}
	_, e = s.Add(ctx, 2, fact(-1, "one"), testNow)
	if !errors.Is(e, daily.ErrDuplicate) {
		t.Fatal(e)
	}
	if _, e = s.Item(ctx, -2, id); e == nil {
		t.Fatal("cross-chat read")
	}
	if e = s.Moderate(ctx, 3, -2, id, 42, true, testNow); !errors.Is(e, daily.ErrConflict) {
		t.Fatal(e)
	}
	must(t, s.Moderate(ctx, 3, -1, id, 42, true, testNow)) // failed mutation did not consume operation
	if e = s.Moderate(ctx, 4, -1, id, 42, true, testNow); !errors.Is(e, daily.ErrConflict) {
		t.Fatal(e)
	}
	_, e = s.Add(ctx, 5, fact(-2, "one"), testNow)
	must(t, e)
}
func TestClaimRecoverAndResolve(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	setup(t, s, -1)
	item, e := s.Add(ctx, 1, fact(-1, "one"), testNow)
	must(t, e)
	must(t, s.Moderate(ctx, 2, -1, item, 42, true, testNow))
	id := reserve(t, s)
	if reserve(t, s) != 0 {
		t.Fatal("duplicate slot")
	}
	must(t, s.Attach(ctx, id, nil, testNow))
	ok, e := s.Claim(ctx, id, testNow.Add(time.Hour))
	must(t, e)
	if !ok {
		t.Fatal("not claimed")
	}
	ok, e = s.Claim(ctx, id, testNow.Add(time.Hour))
	must(t, e)
	if ok {
		t.Fatal("claimed twice")
	}
	must(t, s.Recover(ctx))
	issues, e := s.Issues(ctx, -1)
	must(t, e)
	if len(issues) != 1 || issues[0].State != "unknown" {
		t.Fatal(issues)
	}
	if e = s.Resolve(ctx, 6, -2, id, false); !errors.Is(e, daily.ErrConflict) {
		t.Fatal("cross-chat resolution", e)
	}
	must(t, s.Resolve(ctx, 6, -1, id, false))
	i, e := s.Item(ctx, -1, item)
	must(t, e)
	if i.State != "approved" {
		t.Fatal(i.State)
	}
	if reserve(t, s) != 0 {
		t.Fatal("requeue reused same local-day slot")
	}
}

func TestAttachKeepsAutomaticReserve(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	setup(t, s, -1)
	must(t, s.SetModeration(ctx, 99, -1, false, testNow))
	id := reserve(t, s)
	candidates := []daily.Item{
		fact(-1, "auto:first"),
		fact(-1, "auto:second"),
		fact(-1, "auto:third"),
	}
	must(t, s.Attach(ctx, id, candidates, testNow))
	pending, err := s.Pending(ctx)
	must(t, err)
	if len(pending) != 1 || pending[0].Item.Key != "auto:first" {
		t.Fatal(pending)
	}
	queued, err := s.Queue(ctx, -1, 0)
	must(t, err)
	if len(queued) != 2 || queued[0].Key != "auto:second" || queued[1].Key != "auto:third" {
		t.Fatal(queued)
	}
	approved, err := s.HasApproved(ctx, -1, daily.Fact)
	must(t, err)
	if !approved {
		t.Fatal("automatic reserve was not retained")
	}
}

func TestAttachManualModeQueuesEveryCandidate(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	setup(t, s, -1)
	id := reserve(t, s)
	must(t, s.Attach(ctx, id, []daily.Item{fact(-1, "manual:first"), fact(-1, "manual:second")}, testNow))
	queued, err := s.Queue(ctx, -1, 0)
	must(t, err)
	if len(queued) != 2 || queued[0].State != "pending" || queued[1].State != "pending" {
		t.Fatal(queued)
	}
	issues, err := s.Issues(ctx, -1)
	must(t, err)
	if len(issues) != 1 || issues[0].State != "skipped" || issues[0].Error != "manual_approval_required" {
		t.Fatal(issues)
	}
}
func TestSettingsCancelOnlyAffectedPending(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	setup(t, s, -1)
	item, e := s.Add(ctx, 1, fact(-1, "one"), testNow)
	must(t, e)
	must(t, s.Moderate(ctx, 2, -1, item, 42, true, testNow))
	id := reserve(t, s)
	must(t, s.Attach(ctx, id, nil, testNow))
	must(t, s.SetSchedule(ctx, 3, -1, "meme", "08:45", true, testNow.Add(time.Minute)))
	pending, e := s.Pending(ctx)
	must(t, e)
	if len(pending) != 1 {
		t.Fatal("unrelated schedule cancelled", pending)
	}
	must(t, s.SetSchedule(ctx, 4, -1, "fact", "09:00", false, testNow.Add(2*time.Minute)))
	pending, e = s.Pending(ctx)
	must(t, e)
	if len(pending) != 0 {
		t.Fatal(pending)
	}
	i, e := s.Item(ctx, -1, item)
	must(t, e)
	if i.State != "approved" {
		t.Fatal(i.State)
	}
	must(t, s.SetSchedule(ctx, 5, -1, "meme", "09:00", false, testNow.Add(3*time.Minute)))
	schedules, e := s.Schedules(ctx, -1)
	must(t, e)
	for _, x := range schedules {
		if x.Kind == "meme" && x.Clock != "08:45" {
			t.Fatal("pause erased schedule")
		}
	}
}
func TestRetryCannotUseChangedSchedule(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	setup(t, s, -1)
	item, e := s.Add(ctx, 1, fact(-1, "one"), testNow)
	must(t, e)
	must(t, s.Moderate(ctx, 2, -1, item, 42, true, testNow))
	id := reserve(t, s)
	must(t, s.Attach(ctx, id, nil, testNow))
	_, e = s.Claim(ctx, id, testNow.Add(time.Hour))
	must(t, e)
	must(t, s.SetSchedule(ctx, 3, -1, "fact", "09:30", true, testNow.Add(time.Hour+time.Minute)))
	must(t, s.Finish(ctx, id, "retry", 0, testNow.Add(time.Hour+2*time.Minute).Unix()))
	ok, e := s.Claim(ctx, id, testNow.Add(time.Hour+3*time.Minute))
	must(t, e)
	if ok {
		t.Fatal("old task claimed after rescheduling")
	}
}

func TestAPIBudgetPersistsAcrossRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "budget.db")
	s, err := Open(ctx, path)
	must(t, err)
	allowed, err := s.AllowAPI(ctx, "2026-09-22", 2)
	must(t, err)
	if !allowed {
		t.Fatal("first request refused")
	}
	must(t, s.Close())
	s, err = Open(ctx, path)
	must(t, err)
	defer s.Close()
	// Competing callers cannot exceed the remaining single attempt.
	answers := make(chan bool, 10)
	failures := make(chan error, 10)
	for range 10 {
		go func() { ok, e := s.AllowAPI(ctx, "2026-09-22", 2); answers <- ok; failures <- e }()
	}
	count := 0
	for range 10 {
		if <-answers {
			count++
		}
		must(t, <-failures)
	}
	if count != 1 {
		t.Fatalf("allowed %d instead of one", count)
	}
	allowed, err = s.AllowAPI(ctx, "2026-09-23", 2)
	must(t, err)
	if !allowed {
		t.Fatal("next day did not reset")
	}
	allowed, err = s.AllowAPI(ctx, "2026-09-24", 0)
	must(t, err)
	if allowed {
		t.Fatal("zero limit allowed request")
	}
}

func TestModerationMigrationPreservesExistingData(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	p, err := s.migrator()
	must(t, err)
	_, err = p.DownTo(ctx, 2)
	must(t, err)
	_, err = s.db.Exec("INSERT INTO chats(chat_id,zone) VALUES(-1,'UTC')")
	must(t, err)
	_, err = s.db.Exec(`INSERT INTO items(chat_id,kind,text,source,content_key,author_id,state,created_at,approved_by) VALUES(-1,'fact','Old fact','https://example.org','old',42,'approved',1,42)`)
	must(t, err)
	_, err = p.Up(ctx)
	must(t, err)
	var moderation, ai bool
	var text string
	must(t, s.db.QueryRow("SELECT moderation FROM chats WHERE chat_id=-1").Scan(&moderation))
	must(t, s.db.QueryRow("SELECT text,ai_approved FROM items WHERE chat_id=-1").Scan(&text, &ai))
	if moderation || ai || text != "Old fact" {
		t.Fatal(moderation, ai, text)
	}
	approved, err := s.HasApproved(ctx, -1, daily.Fact)
	must(t, err)
	if approved {
		t.Fatal("legacy human item treated as Gemini approved")
	}
	must(t, s.SetModeration(ctx, 100, -1, true, testNow))
	approved, err = s.HasApproved(ctx, -1, daily.Fact)
	must(t, err)
	if !approved {
		t.Fatal("existing human approval lost")
	}
	_, err = p.DownTo(ctx, 2)
	must(t, err)
	must(t, s.db.QueryRow("SELECT text FROM items WHERE chat_id=-1").Scan(&text))
	if text != "Old fact" {
		t.Fatal("downgrade lost item")
	}
}

func TestModerationPersistsAndIsolatesChats(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "db")
	s, err := Open(ctx, path)
	must(t, err)
	must(t, s.EnsureChat(ctx, -1))
	must(t, s.EnsureChat(ctx, -2))
	must(t, s.SetModeration(ctx, 1, -1, true, testNow))
	must(t, s.Close())
	s, err = Open(ctx, path)
	must(t, err)
	defer s.Close()
	rows, err := s.Schedules(ctx, 0)
	must(t, err)
	for _, r := range rows {
		if r.Moderation != (r.ChatID == -1) {
			t.Fatal(r)
		}
	}
}

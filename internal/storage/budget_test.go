package storage

import (
	"context"
	"path/filepath"
	"testing"
)

func TestProviderBudgetIsolationAndPersistence(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "budget.db")
	s, err := Open(ctx, path)
	must(t, err)
	ok, err := s.AllowAPI(ctx, "2026-09-22", 1)
	must(t, err)
	if !ok {
		t.Fatal("Gemini denied")
	}
	b := ProviderBudget{Store: s, Provider: "groq"}
	ok, err = b.AllowAPI(ctx, "2026-09-22", 2)
	must(t, err)
	if !ok {
		t.Fatal("Groq blocked by Gemini")
	}
	must(t, s.Close())
	s, err = Open(ctx, path)
	must(t, err)
	defer s.Close()
	b.Store = s
	answers := make(chan bool, 10)
	failures := make(chan error, 10)
	for range 10 {
		go func() { ok, e := b.AllowAPI(ctx, "2026-09-22", 2); answers <- ok; failures <- e }()
	}
	n := 0
	for range 10 {
		if <-answers {
			n++
		}
		must(t, <-failures)
	}
	if n != 1 {
		t.Fatalf("remaining budget allowed %d requests", n)
	}
	ok, err = s.AllowAPI(ctx, "2026-09-22", 1)
	must(t, err)
	if ok {
		t.Fatal("existing Gemini budget reset")
	}
	ok, err = b.AllowAPI(ctx, "2026-09-23", 2)
	must(t, err)
	if !ok {
		t.Fatal("next day denied")
	}
	ok, err = b.AllowAPI(ctx, "2026-09-24", 0)
	must(t, err)
	if ok {
		t.Fatal("disabled provider allowed")
	}
}

func TestProviderBudgetMigrationPreservesGeminiCounter(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	p, err := s.migrator()
	must(t, err)
	_, err = p.DownTo(ctx, 3)
	must(t, err)
	_, err = s.db.Exec("INSERT INTO api_usage(utc_date,requests) VALUES('2026-09-22',6)")
	must(t, err)
	_, err = p.Up(ctx)
	must(t, err)
	ok, err := s.AllowAPI(ctx, "2026-09-22", 6)
	must(t, err)
	if ok {
		t.Fatal("migration reset Gemini budget")
	}
	b := ProviderBudget{Store: s, Provider: "groq"}
	ok, err = b.AllowAPI(ctx, "2026-09-22", 6)
	must(t, err)
	if !ok {
		t.Fatal("new provider unavailable")
	}
	_, err = p.DownTo(ctx, 3)
	must(t, err)
	var used int
	must(t, s.db.QueryRow("SELECT requests FROM api_usage WHERE utc_date='2026-09-22'").Scan(&used))
	if used != 6 {
		t.Fatal("downgrade changed Gemini history")
	}
}

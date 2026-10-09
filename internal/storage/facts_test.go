package storage

import (
	"fmt"
	"testing"
	"time"

	"github.com/vaporon4a/movie-helper/internal/facts"
)

func TestFactPolicyUpgradePreservesAmbiguousSendsAndManualItems(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	p, err := s.migrator()
	must(t, err)
	_, err = p.DownTo(ctx, 15)
	must(t, err)
	must(t, s.EnsureChat(ctx, -1))
	states := []string{"ready", "retry", "sending", "unknown", "sent"}
	for n, state := range states {
		_, err = s.db.ExecContext(ctx, `INSERT INTO items(id,chat_id,kind,text,source,content_key,author_id,state,created_at,ai_approved,generation_policy) VALUES(?,-1,'fact','old','https://example.org',?,0,?,0,1,'fact-v4')`, n+1, fmt.Sprint(n), map[bool]string{true: "sent", false: "reserved"}[state == "sent"])
		must(t, err)
		_, err = s.db.ExecContext(ctx, `INSERT INTO deliveries(chat_id,kind,local_date,item_id,deadline,state,fetch_attempts) VALUES(-1,'fact',?,?,9999999999,?,3)`, fmt.Sprint(n), n+1, state)
		must(t, err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO items(chat_id,kind,text,source,content_key,author_id,state,created_at,ai_approved,generation_policy) VALUES(-1,'fact','manual','https://example.org','manual',42,'approved',0,0,'')`)
	must(t, err)
	_, err = p.Up(ctx)
	must(t, err)
	for n, state := range states {
		var actual, itemState string
		var item, attempts int
		must(t, s.db.QueryRowContext(ctx, `SELECT d.state,COALESCE(d.item_id,0),d.fetch_attempts,i.state FROM deliveries d LEFT JOIN items i ON i.id=? WHERE d.local_date=?`, n+1, fmt.Sprint(n)).Scan(&actual, &item, &attempts, &itemState))
		if state == "ready" || state == "retry" {
			if actual != "preparing" || item != 0 || attempts != 0 || itemState != "rejected" {
				t.Fatal(state, actual, item, attempts, itemState)
			}
		} else if actual != state || item != n+1 || attempts != 3 {
			t.Fatal("ambiguous send modified", state, actual, item, attempts)
		}
	}
	var manual string
	must(t, s.db.QueryRowContext(ctx, "SELECT state FROM items WHERE content_key='manual'").Scan(&manual))
	if manual != "approved" {
		t.Fatal(manual)
	}
}
func TestFactRotationAndCachePersist(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	must(t, s.EnsureChat(ctx, -1))
	movies := []facts.Movie{{ID: 1}, {ID: 2}, {ID: 3}}
	picked, err := s.PickFactMovies(ctx, -1, movies, testNow, 2)
	must(t, err)
	if len(picked) != 2 {
		t.Fatal(picked)
	}
	picked, err = s.PickFactMovies(ctx, -1, movies, testNow, 2)
	must(t, err)
	if len(picked) != 1 || picked[0].ID != 3 {
		t.Fatal("rotation lost", picked)
	}
	must(t, s.SaveFactCache(ctx, "x", []byte("snapshot"), testNow.Add(time.Hour)))
	raw, ok, err := s.LoadFactCache(ctx, "x", testNow)
	must(t, err)
	if !ok || string(raw) != "snapshot" {
		t.Fatal(ok, string(raw))
	}
	_, ok, err = s.LoadFactCache(ctx, "x", testNow.Add(time.Hour))
	must(t, err)
	if ok {
		t.Fatal("expired cache returned")
	}
}

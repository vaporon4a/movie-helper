package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestMemeRejectionsPersistExpireAndScope(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "reviews.db")
	s, err := Open(ctx, path)
	must(t, err)
	now := time.Now().Truncate(time.Second)
	must(t, s.RejectMeme(ctx, "groq:model:v2", "post", now))
	must(t, s.Close())
	s, err = Open(ctx, path)
	must(t, err)
	defer s.Close()
	for _, tc := range []struct {
		scope string
		at    time.Time
		want  bool
	}{
		{"groq:model:v2", now, true}, {"gemini:model:v2", now, false}, {"groq:new-model:v2", now, false}, {"groq:model:v2", now.Add(24 * time.Hour), false},
	} {
		got, err := s.MemeRejected(ctx, tc.scope, "post", tc.at)
		must(t, err)
		if got != tc.want {
			t.Fatal(tc, got)
		}
	}
	must(t, s.RejectMeme(ctx, "groq:model:v2", "another", now.Add(25*time.Hour)))
	var count int
	must(t, s.db.QueryRow("SELECT count(*) FROM meme_rejections").Scan(&count))
	if count != 1 {
		t.Fatal("expired rejection not pruned", count)
	}
	p, err := s.migrator()
	must(t, err)
	_, err = p.DownTo(ctx, 4)
	must(t, err)
	_, err = p.Up(ctx)
	must(t, err)
}

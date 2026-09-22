package content

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/vaporon4a/movie-helper/internal/daily"
)

type history map[string]bool

func (h history) Seen(_ context.Context, _ int64, _, key string) (bool, error) { return h[key], nil }

func TestWikipediaOnlyProductionAndRevision(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Query().Get("titles") != "Alien (film)" || r.URL.Query().Get("prop") != "extracts|revisions" || r.Header.Get("User-Agent") == "" {
			t.Error("wrong request")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"query": map[string]any{"pages": []any{map[string]any{"title": "Alien (film)", "extract": "Plot spoiler\n== Plot ==\nmore plot\n== Production ==\n=== Filming ===\nThe production used miniature models and practical effects. The team constructed detailed sets to create the interior of a spaceship.\n== Reception ==\nOpinion", "revisions": []any{map[string]any{"revid": 123}}}}}})
	}))
	defer srv.Close()
	w := &Wikipedia{HTTP: srv.Client(), Endpoint: srv.URL, Titles: []string{"Alien (film)"}, Now: time.Now}
	a, err := w.Articles(context.Background(), -1, history{})
	if err != nil || len(a) != 1 {
		t.Fatalf("articles %v %v", a, err)
	}
	if a[0].URL != "https://en.wikipedia.org/w/index.php?oldid=123" || a[0].Key != "wikipedia:Alien (film)" || a[0].Attribution == "" || a[0].Text[:3] != "The" {
		t.Fatalf("wrong provenance %#v", a[0])
	}
	a, err = w.Articles(context.Background(), -1, history{"wikipedia:Alien (film)": true})
	if err != nil || len(a) != 0 || calls != 1 {
		t.Fatal("seen article fetched again")
	}
}

func TestWikipediaLimitsFetchAndSkipsUnavailable(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(503) }))
	defer srv.Close()
	w := &Wikipedia{HTTP: srv.Client(), Endpoint: srv.URL, Titles: []string{"a", "b", "c", "d"}, Now: time.Now}
	a, err := w.Articles(context.Background(), -1, history{})
	if err == nil || len(a) != 0 || calls != 3 {
		t.Fatalf("%d %v %v", calls, a, err)
	}
	if production("== Plot ==\nfiction\n== Reception ==\nreview") != "" {
		t.Fatal("plot accepted as production")
	}
}

type memes []daily.Item

func (m memes) Candidates(context.Context) ([]daily.Item, error) { return m, nil }
func TestProviderFiltersSeenInChat(t *testing.T) {
	p := &Provider{Memes: memes{{Key: "seen"}, {Key: "new"}}, History: history{"seen": true}, Editor: editor{}}
	got, err := p.Candidates(context.Background(), daily.Meme, -1)
	if err != nil || len(got) != 1 || got[0].Key != "new" {
		t.Fatal(got, err)
	}
	got, err = p.Candidates(context.Background(), daily.Fact, -1)
	if err != nil || len(got) != 0 {
		t.Fatal("facts without Gemini", got, err)
	}
}

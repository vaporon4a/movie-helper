package tmdb

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vaporon4a/movie-helper/internal/clubprofile"
)

func TestResearchSourcesIdentityAndOpinions(t *testing.T) {
	wrong := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/reviews") {
			w.Write([]byte(`{"id":603,"results":[{"id":"review","content":"An opinion, not a plot fact."}]}`))
			return
		}
		if wrong {
			w.Write([]byte(`{"id":603,"title":"Another Matrix","release_date":"1980-01-01"}`))
			return
		}
		w.Write([]byte(`{"id":603,"title":"Матрица","release_date":"1999-03-30","overview":"Описание завязки фильма."}`))
	}))
	defer server.Close()
	c := &Client{HTTP: server.Client(), BaseURL: server.URL, Token: "test"}
	f := clubprofile.Film{TMDBID: 603, Year: 1999}
	sources, err := c.ResearchSources(t.Context(), f, time.Now())
	if err != nil || len(sources) != 2 {
		t.Fatal(sources, err)
	}
	if sources[0].Kind != "plot" || sources[1].Kind != "opinion" {
		t.Fatal("opinions became facts")
	}
	for _, s := range sources {
		if err = s.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	wrong = true
	if _, err = c.ResearchSources(t.Context(), f, time.Now()); err == nil {
		t.Fatal("wrong year accepted")
	}
}

package tmdb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDiscoverUsesBearerAndMovieFilters(t *testing.T) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		if r.URL.Path != "/discover/movie" || r.URL.Query().Get("language") != "ru-RU" ||
			r.URL.Query().Get("region") != "RU" || r.URL.Query().Get("with_genres") != "35" ||
			r.URL.Query().Get("include_adult") != "false" || r.URL.Query().Get("vote_count.gte") != "300" {
			t.Errorf("unexpected request %s", r.URL.String())
		}
		_, _ = w.Write([]byte(`{"results":[{"id":7,"title":"Фильм","overview":"Описание","poster_path":"/p.jpg","release_date":"2024-02-03","vote_average":7.4,"vote_count":400,"popularity":99.5},{"id":0,"title":"bad"}]}`))
	}))
	defer server.Close()

	client := &Client{HTTP: server.Client(), BaseURL: server.URL, Token: "secret"}
	movies, err := client.Discover(context.Background(), 35, 1, 300)
	if err != nil || len(movies) != 1 {
		t.Fatalf("Discover() = %#v, %v", movies, err)
	}
	if movies[0].ID != 7 || movies[0].Year != 2024 || movies[0].PosterPath != "/p.jpg" {
		t.Fatalf("movie = %#v", movies[0])
	}
}

func TestConfigurationBuildsValidatedPosterURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"images":{"secure_base_url":"https://cdn.example/","poster_sizes":["w342","w500"]}}`))
	}))
	defer server.Close()
	client := &Client{HTTP: server.Client(), BaseURL: server.URL, Token: "secret"}
	if err := client.LoadConfiguration(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := client.PosterURL("/poster.jpg"); got != "https://cdn.example/w500/poster.jpg" {
		t.Fatal(got)
	}
}

func TestRejectsHTTPErrorOversizedAndMultipleJSONValues(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
	}{
		"status":    {http.StatusTooManyRequests, `{}`},
		"oversized": {http.StatusOK, strings.Repeat(" ", maxResponseBytes+1)},
		"multiple":  {http.StatusOK, `{"results":[]} {"results":[]}`},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			client := &Client{HTTP: server.Client(), BaseURL: server.URL, Token: "secret"}
			if _, err := client.Discover(context.Background(), 35, 1, 100); err == nil {
				t.Fatal("invalid response accepted")
			}
		})
	}
}

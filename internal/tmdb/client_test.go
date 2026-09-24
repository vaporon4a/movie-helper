package tmdb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vaporon4a/movie-helper/internal/movieclub"
)

func TestDiscoverUsesBearerAndMovieFilters(t *testing.T) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		if r.URL.Path != "/discover/movie" || r.URL.Query().Get("language") != "ru-RU" ||
			r.URL.Query().Has("region") || r.URL.Query().Get("with_genres") != "35" ||
			r.URL.Query().Get("include_adult") != "false" || r.URL.Query().Get("vote_count.gte") != "300" ||
			r.URL.Query().Get("sort_by") != "vote_average.desc" ||
			r.URL.Query().Get("primary_release_date.gte") != "2000-01-01" ||
			r.URL.Query().Get("primary_release_date.lte") != "2009-12-31" {
			t.Errorf("unexpected request %s", r.URL.String())
		}
		_, _ = w.Write([]byte(`{"results":[{"id":7,"title":"Фильм","overview":"Описание","poster_path":"/p.jpg","release_date":"2024-02-03","vote_average":7.4,"vote_count":400,"popularity":99.5},{"id":0,"title":"bad"}]}`))
	}))
	defer server.Close()

	client := &Client{HTTP: server.Client(), BaseURL: server.URL, Token: "secret"}
	query := movieclub.DiscoverQuery{GenreID: 35, Page: 1, MinVotes: 300, FromDate: date(2000, 1, 1), ToDate: date(2009, 12, 31), Sort: movieclub.DiscoverByRating}
	movies, err := client.Discover(context.Background(), query)
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

func TestDiscoverRejectsInvalidQueryBeforeRequest(t *testing.T) {
	client := &Client{HTTP: http.DefaultClient, BaseURL: "http://invalid.example", Token: "secret"}
	valid := movieclub.DiscoverQuery{GenreID: 35, Page: 1, MinVotes: 100, FromDate: date(2000, 1, 1), ToDate: date(2009, 12, 31), Sort: movieclub.DiscoverByRating}
	tests := []movieclub.DiscoverQuery{
		{},
		func() movieclub.DiscoverQuery { value := valid; value.GenreID = 0; return value }(),
		func() movieclub.DiscoverQuery { value := valid; value.Page = 501; return value }(),
		func() movieclub.DiscoverQuery { value := valid; value.MinVotes = -1; return value }(),
		func() movieclub.DiscoverQuery { value := valid; value.ToDate = date(1999, 12, 31); return value }(),
		func() movieclub.DiscoverQuery { value := valid; value.Sort = "popularity.desc"; return value }(),
	}
	for index, query := range tests {
		if _, err := client.Discover(context.Background(), query); err == nil {
			t.Fatalf("query %d accepted: %#v", index, query)
		}
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
			query := movieclub.DiscoverQuery{GenreID: 35, Page: 1, MinVotes: 100, FromDate: date(2000, 1, 1), ToDate: date(2009, 12, 31), Sort: movieclub.DiscoverByRating}
			if _, err := client.Discover(context.Background(), query); err == nil {
				t.Fatal("invalid response accepted")
			}
		})
	}
}

func date(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

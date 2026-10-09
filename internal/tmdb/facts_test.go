package tmdb

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestFactCatalogKeepsPollDiscoveryIndependent(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path == "/discover/movie" && (r.URL.Query().Get("sort_by") != "popularity.desc" || r.URL.Query().Has("with_genres")) {
			t.Error("fact catalog uses poll filters", r.URL.RawQuery)
		}
		_, _ = fmt.Fprintf(w, `{"results":[{"id":%d,"title":"Фильм","original_title":"Film","release_date":"2000-01-01"},{"id":1000,"title":"Будущий","release_date":"2099-01-01"},{"id":2000,"title":"adult","adult":true,"release_date":"2000-01-01"}]}`, calls)
	}))
	defer server.Close()
	client := Client{HTTP: server.Client(), BaseURL: server.URL, Token: "test"}
	movies, err := client.FactMovies(t.Context(), time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC))
	if err != nil || len(movies) != 7 || calls != 7 {
		t.Fatal(movies, calls, err)
	}
}

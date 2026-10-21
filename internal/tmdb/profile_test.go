package tmdb

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWatchCatalogKeepsIdentityAndFullResearchMetadata(t *testing.T) {
	long := strings.Repeat("Не сокращать сюжет. ", 150)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test" {
			t.Error("missing authentication")
		}
		if r.URL.Query().Get("language") != "ru-RU" {
			t.Error("missing Russian locale")
		}
		if r.URL.Path == "/search/movie" {
			if r.URL.Query().Get("include_adult") != "false" || r.URL.Query().Get("query") != "Матрица" {
				t.Error(r.URL.Query())
			}
			w.Write([]byte(`{"results":[{"id":603,"title":"Матрица","release_date":"1999-03-30"},{"id":604,"title":"Матрица","release_date":"1993-01-01"},{"id":0,"title":"bad"}]}`))
			return
		}
		payload := map[string]any{"id": 603, "title": "Матрица", "original_title": "The Matrix", "overview": long, "release_date": "1999-03-30", "runtime": 136, "original_language": "en", "genres": []map[string]any{{"id": 878, "name": "Фантастика"}}, "production_countries": []map[string]string{{"iso_3166_1": "US"}}, "spoken_languages": []map[string]string{{"iso_639_1": "en"}}, "credits": map[string]any{"crew": []map[string]any{{"id": 1, "name": "Лана", "job": "Director"}}}, "keywords": map[string]any{"keywords": []map[string]any{{"id": 2, "name": "simulation"}}}}
		if err := json.NewEncoder(w).Encode(payload); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	c := &Client{HTTP: server.Client(), BaseURL: server.URL, Token: "test"}
	films, err := c.SearchFilms(t.Context(), "Матрица")
	if err != nil || len(films) != 2 || films[0].TMDBID != 603 || films[1].Year != 1993 {
		t.Fatal(films, err)
	}
	identity, err := c.FilmIdentity(t.Context(), 603)
	if err != nil || identity.Metadata.Complete {
		t.Fatal(identity, err)
	}
	details, err := c.FilmDetails(t.Context(), 603)
	if err != nil || !details.Metadata.Complete || details.Metadata.Overview != long || *details.Metadata.Runtime != 136 || details.Metadata.Credits[0].Job != "Director" || details.Metadata.Countries[0] != "US" {
		t.Fatal(details, err)
	}
}

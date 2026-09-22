package meme

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCandidatesFilterAndFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/gimme/broken/10" {
			w.WriteHeader(503)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"memes": []post{
			{Title: "Мем", URL: "https://i.redd.it/a.png", Link: "https://redd.it/a", Ups: 1},
			{Title: "Лучший мем", URL: "https://i.redd.it/b.jpg", Link: "https://redd.it/b", Ups: 5},
			{Title: "Повтор", URL: "https://i.redd.it/a.png", Link: "https://redd.it/a"},
			{Title: "Спойлер", URL: "https://i.redd.it/c.png", Link: "https://redd.it/c", Spoiler: true},
			{Title: "Взрослое", URL: "https://i.redd.it/d.png", Link: "https://redd.it/d", NSFW: true},
			{Title: "English only", URL: "https://i.redd.it/e.png", Link: "https://redd.it/e"},
			{Title: "Локальная ссылка", URL: "http://127.0.0.1/a.png", Link: "https://redd.it/f"},
		}})
	}))
	defer srv.Close()
	c := Client{HTTP: srv.Client(), BaseURL: srv.URL, Subreddits: []string{"broken", "good"}}
	got, err := c.Candidates(context.Background())
	if err != nil || len(got) != 2 || got[0].Key != "reddit:https://redd.it/b" {
		t.Fatal(got, err)
	}
	for _, raw := range []string{"https://i.redd.it.evil/a.png", "https://i.redd.it:443/a.png", "https://u@i.redd.it/a.png", "https://i.redd.it/a.gif", "https://i.redd.it/a.png?url=x"} {
		if validImage(raw) {
			t.Errorf("accepted %s", raw)
		}
	}
}

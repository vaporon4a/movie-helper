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
		if r.URL.Path == "/gimme/broken/50" {
			w.WriteHeader(503)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"memes": []post{
			{Title: "Мем", URL: "https://i.redd.it/a.png", Link: "https://redd.it/a", Ups: 1},
			{Title: "Лучший мем", URL: "https://i.redd.it/b.jpg", Link: "https://redd.it/b", Ups: 5, Preview: []string{
				"https://preview.redd.it/b.jpg?width=320&crop=smart&auto=webp&s=one",
				"https://preview.redd.it/b.jpg?width=1080&crop=smart&auto=webp&s=two",
			}},
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
	if got[0].AnalysisImage != "https://preview.redd.it/b.jpg?width=1080&crop=smart&auto=webp&s=two" {
		t.Fatal(got[0].AnalysisImage)
	}
	for _, raw := range []string{"https://i.redd.it.evil/a.png", "https://i.redd.it:443/a.png", "https://u@i.redd.it/a.png", "https://i.redd.it/a.gif", "https://i.redd.it/a.png?url=x"} {
		if validImage(raw) {
			t.Errorf("accepted %s", raw)
		}
	}
	for _, raw := range []string{
		"https://evil.example/b.jpg?width=1080&s=x",
		"https://preview.redd.it/b.gif?width=1080&s=x",
		"https://preview.redd.it/b.jpg?width=9999&s=x",
		"https://preview.redd.it/b.jpg?width=1080&s=x&url=https://evil.example",
	} {
		if bestPreview([]string{raw}) != "" {
			t.Errorf("accepted preview %s", raw)
		}
	}
}

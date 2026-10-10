package main

import (
	"github.com/vaporon4a/movie-helper/internal/config"
	"github.com/vaporon4a/movie-helper/internal/content"
	"github.com/vaporon4a/movie-helper/internal/gemini"
	"log/slog"
	"net/http"
	"testing"
)

func TestMemeModelDoesNotChangeFactAndTitleGenerator(t *testing.T) {
	provider, primary, _ := buildContentProvider(config.Config{GeminiKey: "test-key", GeminiModel: "gemini-3.8-flash", GeminiMemeModel: "gemini-3.5-flash-lite"}, nil, slog.Default(), func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse })
	fallback, ok := provider.Editor.(*content.Fallback)
	if !ok {
		t.Fatal("missing fallback")
	}
	vision, ok := fallback.Primary.(*gemini.Client)
	if !ok {
		t.Fatal("wrong vision adapter")
	}
	text, ok := primary.(*gemini.Client)
	if !ok {
		t.Fatal("wrong text adapter")
	}
	if vision == text || vision.Model != "gemini-3.5-flash-lite" || text.Model != "gemini-3.8-flash" || vision.Budget != text.Budget {
		t.Fatal("routing or shared budget changed")
	}
}

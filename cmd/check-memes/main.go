// check-memes exercises the real source, image preparation and AI selection
// without Telegram or the production database. Keys come only from environment.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vaporon4a/movie-helper/internal/ai"
	"github.com/vaporon4a/movie-helper/internal/content"
	"github.com/vaporon4a/movie-helper/internal/daily"
	"github.com/vaporon4a/movie-helper/internal/gemini"
	"github.com/vaporon4a/movie-helper/internal/groq"
	"github.com/vaporon4a/movie-helper/internal/meme"
	"github.com/vaporon4a/movie-helper/internal/storage"
)

type editor interface {
	SelectMeme(context.Context, []daily.Item) (*daily.Item, error)
}
type measuredTransport struct{ base http.RoundTripper }

func (t measuredTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	start := time.Now()
	response, err := t.base.RoundTrip(r)
	if r.Method == http.MethodPost {
		status := 0
		if response != nil {
			status = response.StatusCode
		}
		slog.Info("diagnostic AI request", "host", r.URL.Host, "request_bytes", r.ContentLength, "status", status, "duration_ms", time.Since(start).Milliseconds())
	}
	return response, err
}
func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if err := run(); err != nil {
		slog.Error(err.Error())
		os.Exit(1)
	}
}
func run() error {
	provider := flag.String("provider", "groq", "groq or gemini")
	state := flag.String("state", "", "separate diagnostic directory; never use the bot data directory")
	flag.Parse()
	if *state == "" {
		return errors.New("-state is required")
	}
	key, model := os.Getenv("GROQ_API_KEY"), os.Getenv("GROQ_MODEL")
	if model == "" {
		model = "qwen/qwen3.8-27b"
	}
	if *provider == "gemini" {
		key, model = os.Getenv("GEMINI_API_KEY"), os.Getenv("GEMINI_MODEL")
		if model == "" {
			model = "gemini-3.8-flash"
		}
	} else if *provider != "groq" {
		return errors.New("unknown provider")
	}
	if key == "" {
		return errors.New("provider key missing")
	}
	if err := os.MkdirAll(*state, 0700); err != nil {
		return errors.New("cannot create diagnostic directory")
	}
	ctx, cancel := context.WithTimeout(context.Background(), content.FetchTimeout)
	defer cancel()
	store, err := storage.Open(ctx, filepath.Join(*state, "diagnostic.db"))
	if err != nil {
		return errors.New("cannot open diagnostic database")
	}
	defer func() { _ = store.Close() }()
	requestTimeout, selectionTimeout := content.GroqRequestTimeout, content.GroqSelectionTimeout
	if *provider == "gemini" {
		requestTimeout, selectionTimeout = content.GeminiRequestTimeout, content.GeminiSelectionTimeout
	}
	h := &http.Client{Timeout: requestTimeout, Transport: measuredTransport{http.DefaultTransport}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var items []daily.Item
	path := filepath.Join(*state, "candidates.json")
	// #nosec G304 -- path is confined to the explicitly supplied diagnostic directory.
	data, err := os.ReadFile(path)
	if err == nil {
		if json.Unmarshal(data, &items) != nil {
			return errors.New("invalid candidate fixture")
		}
	} else if !os.IsNotExist(err) {
		return errors.New("cannot read candidate fixture")
	} else {
		subs := os.Getenv("MEME_SUBREDDITS")
		if subs == "" {
			subs = "RUSSIANMemeSub"
		}
		client := &meme.Client{HTTP: h, BaseURL: "https://meme-api.com", Subreddits: strings.Split(subs, ",")}
		items, err = client.Candidates(ctx)
		if err != nil {
			return err
		}
		data, err = json.Marshal(items)
		if err != nil {
			return err
		}
		if os.WriteFile(path, data, 0600) != nil {
			return errors.New("cannot save candidate fixture")
		}
	}
	budget := storage.ProviderBudget{Store: store, Provider: *provider}
	var ed editor
	if *provider == "groq" {
		ed = &groq.Client{HTTP: h, BaseURL: "https://api.groq.com/openai/v1", Key: key, Model: model, Budget: budget, DailyLimit: 6, Now: time.Now, Reviews: store}
	} else {
		ed = &gemini.Client{HTTP: h, BaseURL: "https://generativelanguage.googleapis.com/v1beta", Key: key, Model: model, Budget: budget, DailyLimit: 6, Now: time.Now, Reviews: store}
	}
	slog.Info("diagnostic start", "provider", *provider, "model", model, "candidates", len(items))
	attempt, cancelAttempt := context.WithTimeout(ctx, selectionTimeout)
	defer cancelAttempt()
	item, err := ed.SelectMeme(attempt, items)
	if err != nil {
		if errors.Is(err, ai.ErrDailyLimit) {
			return fmt.Errorf("diagnostic daily budget exhausted for %s", *provider)
		}
		return err
	}
	if item == nil {
		slog.Info("diagnostic complete", "selected", false)
	} else {
		slog.Info("diagnostic complete", "selected", true, "source", item.Source, "image", item.Image)
	}
	return nil
}

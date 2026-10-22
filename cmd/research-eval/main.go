// Command research-eval runs one preparation stage without Telegram delivery or
// schema migrations. Only the existing shared API budget tables are writable.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/vaporon4a/movie-helper/internal/ai"
	"github.com/vaporon4a/movie-helper/internal/aiwork"
	"github.com/vaporon4a/movie-helper/internal/content"
	"github.com/vaporon4a/movie-helper/internal/factsource"
	"github.com/vaporon4a/movie-helper/internal/filmresearch"
	"github.com/vaporon4a/movie-helper/internal/gemini"
	"github.com/vaporon4a/movie-helper/internal/groq"
	"github.com/vaporon4a/movie-helper/internal/tmdb"
	_ "modernc.org/sqlite"
)

type checkpoint struct{ job filmresearch.Job }

func (c *checkpoint) ResearchJob(context.Context, int64) (filmresearch.Job, error) { return c.job, nil }
func (c *checkpoint) CheckpointResearch(_ context.Context, j filmresearch.Job, next string, _ time.Time) error {
	j.Stage = next
	j.Version++
	c.job = j
	return nil
}

type budget struct {
	db       *sql.DB
	provider string
	used     bool
}

func (b *budget) AllowAPI(ctx context.Context, date string, limit int) (bool, error) {
	if b.used || limit <= 0 {
		return false, nil
	}
	b.used = true
	var result sql.Result
	var err error
	if b.provider == "gemini" {
		result, err = b.db.ExecContext(ctx, `INSERT INTO api_usage(utc_date,requests) VALUES(?,1) ON CONFLICT(utc_date) DO UPDATE SET requests=requests+1 WHERE requests<?`, date, limit)
	} else {
		result, err = b.db.ExecContext(ctx, `INSERT INTO provider_api_usage(provider,utc_date,requests) VALUES('groq',?,1) ON CONFLICT(provider,utc_date) DO UPDATE SET requests=requests+1 WHERE requests<?`, date, limit)
	}
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}
func main() {
	if err := run(); err != nil {
		slog.Error("research evaluation stopped", "reason", "evaluation_failed")
		os.Exit(1)
	}
}
func run() error {
	input := flag.String("input", "", "Existing job JSON; omit to collect sources")
	output := flag.String("output", "", "New output file, never overwritten")
	movie := flag.Int64("movie", 0, "TMDB ID for a new source collection")
	provider := flag.String("provider", "groq", "groq or gemini")
	model := flag.String("model", "openai/gpt-oss-120b", "Explicit model ID")
	limit := flag.Int("limit", 16, "Shared daily counter ceiling, maximum 20; default preserves four calls")
	flag.Parse()
	if *output == "" || *limit < 1 || *limit > 20 || (*provider != "groq" && *provider != "gemini") {
		return errors.New("invalid evaluation options")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 115*time.Second)
	defer cancel()
	httpClient := &http.Client{Timeout: 110 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	catalog := &tmdb.Client{HTTP: httpClient, BaseURL: "https://api.themoviedb.org/3", Token: os.Getenv("TMDB_API_TOKEN")}
	job, err := loadEvaluationJob(ctx, *input, *movie, catalog)
	if err != nil {
		return err
	}
	store := &checkpoint{job: job}
	recorder := &recordingGenerator{}
	r := content.ResearchPreparer{Store: store, Sources: []filmresearch.Source{catalog, &factsource.Source{HTTP: httpClient, IDs: catalog, WikidataURL: "https://www.wikidata.org", WikipediaURL: "https://ru.wikipedia.org"}}, Log: slog.Default()}
	if store.job.Stage != "sources" {
		db, err := sql.Open("sqlite", os.Getenv("DB_PATH")+"?_pragma=busy_timeout(5000)")
		if err != nil {
			return err
		}
		defer func() { _ = db.Close() }()
		b := &budget{db: db, provider: *provider}
		recorder.Generator = evaluationGenerator(*provider, *model, httpClient, b, *limit)
		g := recorder
		if *provider == "gemini" {
			r.Primary = g
		} else {
			r.Secondary = g
		}
	}
	out, err := r.Execute(ctx, aiwork.Work{ScopeID: 1, Attempts: 1}, time.Now())
	if err != nil {
		return err
	}
	if err = writeEvaluation(*output, store.job); err != nil {
		return err
	}
	if err = writeEvaluation(*output+".response.json", recorder.response); err != nil {
		return err
	}
	fmt.Printf("movie=%d stage=%s done=%t reason=%s retry=%s\n", store.job.Film.TMDBID, store.job.Stage, out.Done, out.Reason, out.After)
	return nil
}
func evaluationGenerator(provider, model string, httpClient *http.Client, b ai.Budget, limit int) ai.Generator {
	if provider == "gemini" {
		return &gemini.Client{HTTP: httpClient, Now: time.Now, Temperature: new(0.3), BaseURL: "https://generativelanguage.googleapis.com/v1beta", Key: os.Getenv("GEMINI_API_KEY"), Model: model, Budget: b, DailyLimit: limit, InlineRetryLimit: time.Nanosecond, Log: slog.Default()}
	}
	return &groq.Client{HTTP: httpClient, Now: time.Now, Temperature: new(0.3), BaseURL: "https://api.groq.com/openai/v1", Key: os.Getenv("GROQ_API_KEY"), Model: model, ReasoningEffort: "low", MaxCompletionTokens: 4096, Budget: b, DailyLimit: limit, InlineRetryLimit: time.Nanosecond, Log: slog.Default()}
}

func loadEvaluationJob(ctx context.Context, input string, movie int64, catalog *tmdb.Client) (filmresearch.Job, error) {
	if input == "" {
		f, err := catalog.FilmIdentity(ctx, movie)
		return filmresearch.Job{SessionID: 1, ChatID: -1, WatchID: 1, WatchRevision: 1, Film: f, Stage: "sources", Version: 1}, err
	}
	// #nosec G304 -- explicit operator-owned CLI input, never a chat path.
	raw, err := os.ReadFile(input)
	if err != nil {
		return filmresearch.Job{}, err
	}
	var job filmresearch.Job
	err = json.Unmarshal(raw, &job)
	return job, err
}
func writeEvaluation(path string, value any) error {
	// #nosec G304 -- explicit operator output; O_EXCL prevents overwrite.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(value)
}

type evaluationResponse struct {
	Structured json.RawMessage `json:"structured,omitempty"`
	Status     int             `json:"http_status,omitempty"`
}
type recordingGenerator struct {
	ai.Generator
	response evaluationResponse
}

func (r *recordingGenerator) ModelName() string {
	if m, ok := r.Generator.(interface{ ModelName() string }); ok {
		return m.ModelName()
	}
	return "unknown"
}
func (r *recordingGenerator) Generate(ctx context.Context, prompt string, parts []ai.Part) (ai.Selection, error) {
	result, err := r.Generator.Generate(ctx, prompt, parts)
	r.response.Structured = result.Structured
	if g, ok := errors.AsType[*groq.HTTPError](err); ok {
		r.response.Status = g.Status
	}
	if g, ok := errors.AsType[*gemini.HTTPError](err); ok {
		r.response.Status = g.Status
	}
	return result, err
}

func (r *recordingGenerator) GenerationSettings() (string, *float64, int) {
	if s, ok := r.Generator.(interface {
		GenerationSettings() (string, *float64, int)
	}); ok {
		return s.GenerationSettings()
	}
	return "", nil, 0
}

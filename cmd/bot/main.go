package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/vaporon4a/movie-helper/internal/config"
	"github.com/vaporon4a/movie-helper/internal/content"
	"github.com/vaporon4a/movie-helper/internal/daily"
	"github.com/vaporon4a/movie-helper/internal/gemini"
	"github.com/vaporon4a/movie-helper/internal/groq"
	"github.com/vaporon4a/movie-helper/internal/meme"
	"github.com/vaporon4a/movie-helper/internal/movieclub"
	"github.com/vaporon4a/movie-helper/internal/scheduler"
	"github.com/vaporon4a/movie-helper/internal/storage"
	"github.com/vaporon4a/movie-helper/internal/telegram"
	"github.com/vaporon4a/movie-helper/internal/tmdb"
)

func main() {
	if err := run(); err != nil {
		slog.Error(err.Error())
		os.Exit(1)
	}
}
func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.Level}))
	slog.SetDefault(log)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	lock, err := lockDB(cfg.DBPath)
	if err != nil {
		return err
	}
	defer lock.Close()
	store, err := storage.Open(ctx, cfg.DBPath)
	if err != nil {
		return errors.New("cannot open or migrate database")
	}
	defer func() {
		if err := store.Close(); err != nil {
			log.Warn("database close failed")
		}
	}()
	if err = store.Recover(ctx); err != nil {
		return errors.New("cannot recover delivery state")
	}
	if err = store.RecoverMovieRounds(ctx); err != nil {
		return errors.New("cannot recover movie poll state")
	}
	var handler *telegram.Handler
	b, err := bot.New(cfg.Token, bot.WithDefaultHandler(func(handlerCtx context.Context, telegramBot *bot.Bot, update *models.Update) {
		if handler != nil {
			handler.Handle(handlerCtx, telegramBot, update)
		}
	}), bot.WithWorkers(1), bot.WithNotAsyncHandlers(),
		bot.WithHTTPClient(25*time.Second, &http.Client{Timeout: 35 * time.Second}),
		bot.WithAllowedUpdates(bot.AllowedUpdates{"message", "callback_query", "my_chat_member", "poll"}),
		bot.WithErrorsHandler(func(err error) { log.Warn("telegram polling error") }))
	if err != nil {
		return errors.New("telegram initialization failed; check token and connection")
	}
	me, err := b.GetMe(ctx)
	if err != nil {
		return errors.New("cannot identify Telegram bot")
	}
	// Do not silently delete a webhook belonging to an existing deployment.
	wh, err := b.GetWebhookInfo(ctx)
	if err != nil {
		return errors.New("cannot check webhook state")
	}
	if wh.URL != "" {
		return errors.New("webhook is active; remove it before running polling")
	}
	redirect := func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	provider := buildContentProvider(cfg, store, log, redirect)

	dailyService, err := daily.NewService(store, provider, log)
	if err != nil {
		return err
	}
	handler, err = telegram.NewHandler(b, dailyService, cfg.Chats, me.Username, log, time.Now)
	if err != nil {
		return err
	}
	s, err := scheduler.New(store, telegram.Sender{API: b}, provider, cfg.Chats, log, time.Now)
	if err != nil {
		return err
	}
	var workers sync.WaitGroup
	workers.Go(func() { s.Run(ctx) })
	if err = startMovieClub(ctx, cfg, store, b, handler, log, redirect, &workers); err != nil {
		return err
	}
	log.Info("bot started", "allowed_chats", len(cfg.Chats), "gemini_model", cfg.GeminiModel, "gemini_enabled", cfg.GeminiKey != "", "groq_model", cfg.GroqModel, "groq_enabled", cfg.GroqKey != "", "tmdb_enabled", cfg.TMDBToken != "")
	b.Start(ctx)
	cancel()
	workers.Wait()
	log.Info("bot stopped")
	return nil
}

func buildContentProvider(cfg config.Config, store *storage.Store, log *slog.Logger, redirect func(*http.Request, []*http.Request) error) *content.Provider {
	sourceHTTP := &http.Client{Timeout: 12 * time.Second, CheckRedirect: redirect}
	memes := &meme.Client{HTTP: sourceHTTP, BaseURL: "https://meme-api.com", Subreddits: cfg.Subreddits}
	provider := &content.Provider{
		Memes: memes, History: store,
		Facts: &content.Wikipedia{HTTP: sourceHTTP, Endpoint: "https://en.wikipedia.org/w/api.php", Titles: cfg.FactWikiTitles},
	}
	fallback := &content.Fallback{Log: log, PrimaryTimeout: content.GeminiSelectionTimeout, SecondaryTimeout: content.GroqSelectionTimeout}
	if cfg.GeminiKey != "" {
		fallback.Primary = &gemini.Client{HTTP: &http.Client{Timeout: content.GeminiRequestTimeout, CheckRedirect: redirect}, BaseURL: "https://generativelanguage.googleapis.com/v1beta", Key: cfg.GeminiKey, Reviews: store, Model: cfg.GeminiModel, Budget: store, DailyLimit: cfg.GeminiDailyLimit, Now: time.Now}
	}
	if cfg.GroqKey != "" {
		fallback.Secondary = &groq.Client{HTTP: &http.Client{Timeout: content.GroqRequestTimeout, CheckRedirect: redirect}, BaseURL: "https://api.groq.com/openai/v1", Key: cfg.GroqKey, Reviews: store, Model: cfg.GroqModel, Budget: storage.ProviderBudget{Store: store, Provider: "groq"}, DailyLimit: cfg.GroqDailyLimit, Now: time.Now}
	}
	if fallback.Primary != nil || fallback.Secondary != nil {
		provider.Editor = fallback
	} else {
		log.Warn("AI disabled: automatic publishing unavailable")
	}
	return provider
}

func startMovieClub(ctx context.Context, cfg config.Config, store *storage.Store, api telegram.API, handler *telegram.Handler, log *slog.Logger, redirect func(*http.Request, []*http.Request) error, workers *sync.WaitGroup) error {
	if cfg.TMDBToken == "" {
		log.Warn("movie polls disabled: TMDB_API_TOKEN is missing")
		return nil
	}
	tmdbClient := &tmdb.Client{HTTP: &http.Client{Timeout: 15 * time.Second, CheckRedirect: redirect}, BaseURL: "https://api.themoviedb.org/3", Token: cfg.TMDBToken, Log: log}
	loadCtx, loadCancel := context.WithTimeout(ctx, 10*time.Second)
	defer loadCancel()
	if err := tmdbClient.LoadConfiguration(loadCtx); err != nil {
		log.Warn("tmdb image configuration unavailable; using default image host")
	}
	movieSender, err := telegram.NewMovieSender(api, tmdbClient.PosterURL, log)
	if err != nil {
		return err
	}
	genreScenario, err := movieclub.NewGenreScenario(tmdbClient, store)
	if err != nil {
		return err
	}
	referenceScenario, err := movieclub.NewReferenceScenario(tmdbClient, store)
	if err != nil {
		return err
	}
	scenarios, err := movieclub.NewScenarioSet(genreScenario, referenceScenario)
	if err != nil {
		return err
	}
	handler.MovieClub, err = movieclub.NewService(store, movieSender, scenarios, log, time.Now)
	if err != nil {
		return err
	}
	coordinator, err := movieclub.NewCoordinator(store, movieSender, scenarios, cfg.Chats, log, time.Now)
	if err != nil {
		return err
	}
	workers.Go(func() { coordinator.Run(ctx) })
	return nil
}

// The deployment target is Linux. flock prevents two processes from recovering
// each other's in-flight deliveries when they accidentally share a volume.
func lockDB(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, errors.New("cannot create data directory")
	}
	// #nosec G304 -- DB_PATH is administrator-configured and the parent is created mode 0700.
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, errors.New("cannot open database lock")
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if closeErr := f.Close(); closeErr != nil {
			return nil, errors.New("cannot close database lock")
		}
		return nil, errors.New("database is already in use")
	}
	return f, nil
}

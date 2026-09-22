package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/go-telegram/bot"
	"github.com/vaporon4a/movie-helper/internal/config"
	"github.com/vaporon4a/movie-helper/internal/content"
	"github.com/vaporon4a/movie-helper/internal/gemini"
	"github.com/vaporon4a/movie-helper/internal/meme"
	"github.com/vaporon4a/movie-helper/internal/scheduler"
	"github.com/vaporon4a/movie-helper/internal/storage"
	"github.com/vaporon4a/movie-helper/internal/telegram"
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
	defer store.Close()
	if err = store.Recover(ctx); err != nil {
		return errors.New("cannot recover delivery state")
	}
	h := &telegram.Handler{Store: store, Allowed: cfg.Chats, Log: log, Now: time.Now}
	b, err := bot.New(cfg.Token, bot.WithDefaultHandler(h.Handle), bot.WithWorkers(1), bot.WithNotAsyncHandlers(),
		bot.WithHTTPClient(25*time.Second, &http.Client{Timeout: 35 * time.Second}),
		bot.WithAllowedUpdates(bot.AllowedUpdates{"message", "callback_query", "my_chat_member"}),
		bot.WithErrorsHandler(func(err error) { log.Warn("telegram polling error") }))
	if err != nil {
		return errors.New("Telegram initialization failed; check token and connection")
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
	h.API = b
	h.Username = me.Username
	client := &meme.Client{HTTP: &http.Client{Timeout: 12 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}, BaseURL: "https://meme-api.com", Subreddits: cfg.Subreddits}
	provider := &content.Provider{Memes: client, History: store, Facts: &content.Wikipedia{HTTP: client.HTTP, Endpoint: "https://en.wikipedia.org/w/api.php", Titles: cfg.FactWikiTitles, Now: time.Now}}
	if cfg.GeminiKey != "" {
		provider.Editor = &gemini.Client{HTTP: &http.Client{Timeout: 40 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, BaseURL: "https://generativelanguage.googleapis.com/v1beta", Key: cfg.GeminiKey, Model: cfg.GeminiModel, Budget: store, DailyLimit: cfg.GeminiDailyLimit, Now: time.Now}
	} else {
		log.Warn("Gemini disabled: automatic publishing unavailable")
	}
	h.Provider = provider
	s := &scheduler.Scheduler{Store: store, Sender: telegram.Sender{API: b}, Provider: provider, Allowed: cfg.Chats, Log: log, Now: time.Now}
	done := make(chan struct{})
	go func() { defer close(done); s.Run(ctx) }()
	log.Info("bot started", "allowed_chats", len(cfg.Chats))
	b.Start(ctx)
	cancel()
	<-done
	log.Info("bot stopped")
	return nil
}

// The deployment target is Linux. flock prevents two processes from recovering
// each other's in-flight deliveries when they accidentally share a volume.
func lockDB(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, errors.New("cannot create data directory")
	}
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, errors.New("cannot open database lock")
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, errors.New("database is already in use")
	}
	return f, nil
}

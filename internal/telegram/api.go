// Package telegram translates Telegram updates to chat-scoped application actions.
package telegram

import (
	"context"
	"log/slog"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/vaporon4a/movie-helper/internal/daily"
	"github.com/vaporon4a/movie-helper/internal/movieclub"
)

type API interface {
	SendMessage(context.Context, *bot.SendMessageParams) (*models.Message, error)
	SendPhoto(context.Context, *bot.SendPhotoParams) (*models.Message, error)
	SendPoll(context.Context, *bot.SendPollParams) (*models.Message, error)
	StopPoll(context.Context, *bot.StopPollParams) (*models.Poll, error)
	SendMediaGroup(context.Context, *bot.SendMediaGroupParams) ([]*models.Message, error)
	GetChatAdministrators(context.Context, *bot.GetChatAdministratorsParams) ([]models.ChatMember, error)
	AnswerCallbackQuery(context.Context, *bot.AnswerCallbackQueryParams) (bool, error)
}

type DailyRepository interface {
	EnsureChat(context.Context, int64) error
	Suspend(context.Context, int64) error
	Schedules(context.Context, int64) ([]daily.Schedule, error)
	Issues(context.Context, int64) ([]daily.Delivery, error)
	SetModeration(context.Context, int64, int64, bool, time.Time) error
	SetZone(context.Context, int64, int64, string, time.Time) error
	SetSchedule(context.Context, int64, int64, string, string, bool, time.Time) error
	Resume(context.Context, int64, int64) error
	Add(context.Context, int64, daily.Item, time.Time) (int64, error)
	Queue(context.Context, int64, int64) ([]daily.Item, error)
	Item(context.Context, int64, int64) (daily.Item, error)
	Moderate(context.Context, int64, int64, int64, int64, bool, time.Time) error
	Resolve(context.Context, int64, int64, int64, bool) error
}

type DailyApplication interface {
	DailyRepository
	Candidates(context.Context, string, int64) ([]daily.Item, error)
}

type MovieClub interface {
	Start(context.Context, int64, int64, time.Duration) (int64, error)
	SetSchedule(context.Context, int64, int64, int, string, bool) error
	PauseSchedules(context.Context, int64, int64) error
	Settings(context.Context, int64) (movieclub.SettingsView, error)
	PollClosed(context.Context, string, []int) error
	More(context.Context, int64, int64) error
	Resolve(context.Context, int64, int64, int64, movieclub.ResolveAction) error
}

type Handler struct {
	API       API
	MovieClub MovieClub
	Daily     DailyApplication
	Allowed   map[int64]bool
	Username  string
	Log       *slog.Logger
	Now       func() time.Time
}

func NewHandler(api API, dailyApp DailyApplication, allowed map[int64]bool, username string, log *slog.Logger, now func() time.Time) (*Handler, error) {
	if api == nil || dailyApp == nil {
		return nil, errMissingDependency
	}
	if log == nil {
		log = slog.Default()
	}
	if now == nil {
		now = time.Now
	}
	return &Handler{API: api, Daily: dailyApp, Allowed: allowed, Username: username, Log: log, Now: now}, nil
}

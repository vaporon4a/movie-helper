// Package telegram translates Telegram updates to chat-scoped application actions.
package telegram

import (
	"context"
	"log/slog"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/vaporon4a/movie-helper/internal/clubreminder"
	"github.com/vaporon4a/movie-helper/internal/daily"
	"github.com/vaporon4a/movie-helper/internal/featurevote"
	"github.com/vaporon4a/movie-helper/internal/movieclub"
)

// API is used only to assemble narrower consumer ports.
type API interface {
	MessageAPI
	PhotoAPI
	PollAPI
	AdminAPI
}

type MessageAPI interface {
	SendMessage(context.Context, *bot.SendMessageParams) (*models.Message, error)
	EditMessageText(context.Context, *bot.EditMessageTextParams) (*models.Message, error)
	AnswerCallbackQuery(context.Context, *bot.AnswerCallbackQueryParams) (bool, error)
}

type PhotoAPI interface {
	SendPhoto(context.Context, *bot.SendPhotoParams) (*models.Message, error)
	SendMediaGroup(context.Context, *bot.SendMediaGroupParams) ([]*models.Message, error)
}

type PollAPI interface {
	SendPoll(context.Context, *bot.SendPollParams) (*models.Message, error)
	StopPoll(context.Context, *bot.StopPollParams) (*models.Poll, error)
}

type AdminAPI interface {
	GetChatAdministrators(context.Context, *bot.GetChatAdministratorsParams) ([]models.ChatMember, error)
}

// DailyRepository is used only to assemble narrower consumer ports.
type DailyRepository interface {
	DailyChatApplication
	DailyScheduleApplication
	DailyQueueApplication
}

type DailyChatApplication interface {
	EnsureChat(context.Context, int64) error
	Suspend(context.Context, int64) error
	SetZone(context.Context, int64, int64, string, time.Time) error
	Resume(context.Context, int64, int64) error
}

type DailyScheduleApplication interface {
	Schedules(context.Context, int64) ([]daily.Schedule, error)
	Issues(context.Context, int64) ([]daily.Delivery, error)
	SetModeration(context.Context, int64, int64, bool, time.Time) error
	SetSchedule(context.Context, int64, int64, string, string, bool, time.Time) error
}

type DailyQueueApplication interface {
	Add(context.Context, int64, daily.Item, time.Time) (int64, error)
	Queue(context.Context, int64, int64) ([]daily.Item, error)
	Item(context.Context, int64, int64) (daily.Item, error)
	Moderate(context.Context, int64, int64, int64, int64, bool, time.Time) error
	Resolve(context.Context, int64, int64, int64, bool) error
}

type DailyApplication interface {
	DailyRepository
	Candidates(context.Context, string, int64) ([]daily.Item, error)
	Stocks(context.Context, int64, time.Time) ([]daily.Stock, error)
	RequestRefill(context.Context, int64, string, time.Time) error
}

// MovieClub is used only to assemble narrower consumer ports.
type MovieClub interface {
	MoviePollApplication
	MovieResultApplication
	MovieTasteApplication
}

type MoviePollApplication interface {
	Start(context.Context, movieclub.Feature, int64, int64, time.Duration) (int64, error)
	SetSchedule(context.Context, movieclub.Feature, int64, int64, int, string, bool) error
	PauseSchedules(context.Context, movieclub.Feature, int64, int64) error
	Settings(context.Context, int64) (movieclub.SettingsView, error)
	PollClosed(context.Context, string, []int) error
}

type MovieResultApplication interface {
	More(context.Context, int64, int64) (movieclub.Summary, error)
	Resolve(context.Context, int64, int64, int64, movieclub.ResolveAction) error
}

type MovieTasteApplication interface {
	SetPersonalization(context.Context, int64, int64, movieclub.PersonalizationMode) error
	Taste(context.Context, int64) (movieclub.PreferenceSettings, movieclub.ChatTasteProfile, error)
	ResetTaste(context.Context, int64, int64) error
}

// FeatureVotes is used only to assemble narrower consumer ports.
type FeatureVotes interface {
	FeatureIdeaApplication
	FeatureScheduleApplication
	FeatureBallotApplication
}

type FeatureIdeaApplication interface {
	Add(context.Context, int64, int64, int64, string) (featurevote.Idea, error)
	Mine(context.Context, int64, int64) ([]featurevote.Idea, error)
	Backlog(context.Context, int64) ([]featurevote.Idea, error)
	ChangeState(context.Context, int64, int64, int64, int64, featurevote.IdeaState, bool) error
}

type FeatureScheduleApplication interface {
	SetSchedule(context.Context, int64, int64, int, string) error
	Pause(context.Context, int64, int64) error
	Settings(context.Context, int64) (featurevote.SettingsView, error)
}

type FeatureBallotApplication interface {
	Start(context.Context, int64, int64, time.Duration) (int64, error)
	View(context.Context, string, int64) (featurevote.View, error)
	Vote(context.Context, string, int64, int64) error
	PollClosed(context.Context, string, []int) error
	Resolve(context.Context, int64, int64, int64, featurevote.ResolveAction) error
}

type ClubReminders interface {
	SetSchedule(context.Context, int64, int64, int, int, string) error
	Pause(context.Context, int64, int64) error
	Settings(context.Context, int64) (clubreminder.Settings, error)
	Preview(context.Context, int64) (clubreminder.Text, error)
	Resolve(context.Context, int64, int64, int64, string) error
}

type Handler struct {
	Club      ClubReminders
	API       API
	MovieClub MovieClub
	Features  FeatureVotes
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

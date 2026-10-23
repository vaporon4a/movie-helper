// Package telegram translates Telegram updates to chat-scoped application actions.
package telegram

import (
	"context"
	"log/slog"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/vaporon4a/movie-helper/internal/clubprofile"
	"github.com/vaporon4a/movie-helper/internal/clubreminder"
	"github.com/vaporon4a/movie-helper/internal/daily"
	"github.com/vaporon4a/movie-helper/internal/featurevote"
	"github.com/vaporon4a/movie-helper/internal/feedback"
)

// API is used only to assemble narrower consumer ports.
type API interface {
	MessageAPI
	PhotoAPI
	PollAPI
	PanelAPI
}

type PanelAPI interface {
	DeleteAPI
	EditMessageReplyMarkup(context.Context, *bot.EditMessageReplyMarkupParams) (*models.Message, error)
}

type MessageAPI interface {
	SendMessage(context.Context, *bot.SendMessageParams) (*models.Message, error)
	EditMessageText(context.Context, *bot.EditMessageTextParams) (*models.Message, error)
	AnswerCallbackQuery(context.Context, *bot.AnswerCallbackQueryParams) (bool, error)
}

type PhotoAPI interface {
	SendPhoto(context.Context, *bot.SendPhotoParams) (*models.Message, error)
}

type PollAPI interface {
	SendPoll(context.Context, *bot.SendPollParams) (*models.Message, error)
	StopPoll(context.Context, *bot.StopPollParams) (*models.Poll, error)
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
	ChangeState(context.Context, int64, int64, int64, featurevote.IdeaState) error
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

type FeedbackRecorder interface {
	RegisterFeedbackPost(context.Context, feedback.Post) error
	RecordFeedback(context.Context, feedback.Event) (bool, error)
}

type Handler struct {
	Profile  ProfileApplication
	Watches  WatchApplication
	Feedback FeedbackRecorder
	Club     ClubReminders
	API      API
	Features FeatureVotes
	Daily    DailyApplication
	Allowed  map[int64]bool
	Username string
	Log      *slog.Logger
	Now      func() time.Time
}

type ProfileApplication interface {
	Start(context.Context, clubprofile.Topic, int64, int64, time.Duration) (int64, error)
	SetSchedule(context.Context, int64, clubprofile.Schedule) error
	PauseSchedules(context.Context, int64, int64, clubprofile.Topic) error
	Settings(context.Context, int64) ([]clubprofile.Schedule, []clubprofile.Poll, error)
	PollClosed(context.Context, clubprofile.PollResult) error
	Resolve(context.Context, int64, int64, int64, string) error
	Status(context.Context, int64) (clubprofile.ProfileStatus, error)
}

type WatchApplication interface {
	WatchCardApplication
	Add(context.Context, int64, int64, string) (clubprofile.WatchDraft, error)
	Choose(context.Context, int64, int64, int64) (clubprofile.WatchDraft, error)
	History(context.Context, int64) ([]clubprofile.WatchEvent, []clubprofile.ReviewSession, error)
	Change(context.Context, int64, int64, int64, string, string) error
	AutoReview(context.Context, int64, int64, bool) error
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

type WatchCardApplication interface {
	CloseMessage(context.Context, int64, int) error
	BindMessage(context.Context, int64, int64, int) error
	ClaimMessage(context.Context, int64, int64) (bool, error)
	RetryMessage(context.Context, int64, int64, time.Duration) error
}

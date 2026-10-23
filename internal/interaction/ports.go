package interaction

import (
	"context"
	"time"
)

type IntentRepository interface {
	CancelDailyActorInputs(context.Context, int64, int64) ([]int, error)
	CreateDailyIntent(context.Context, Intent, time.Time) error
	DailyIntent(context.Context, string, int64, int, time.Time) (Intent, error)
	BindDailyPrompt(context.Context, string, int) error
	DailyReplyIntent(context.Context, int64, int64, int, time.Time) (Intent, error)
	CancelDailyIntents(context.Context, int64, int, time.Time) ([]int, error)
}
type PanelRepository interface {
	PanelScreen(context.Context, int64, int) (string, error)
	SetPanelScreen(context.Context, int64, int, string) error
	CloseDailyPanel(context.Context, int64, int) error
	DailyPanelClosed(context.Context, int64, int) (bool, error)
}

type Repository interface {
	IntentRepository
	PanelRepository
}

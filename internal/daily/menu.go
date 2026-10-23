package daily

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"time"
)

// ConfigSnapshot keeps raw schedule flags separate from technical chat activity.
type ConfigSnapshot struct {
	Revision  int64
	Active    bool
	Zone      string
	Schedules []Schedule
}
type MenuIntent struct {
	Token             string
	ChatID            int64
	PanelID           int
	Kind, Action      string
	Revision, ActorID int64
	PromptID          int
	Clock, State      string
	Expires           time.Time
}
type ConfigMenuRepository interface {
	DailyConfig(context.Context, int64) (ConfigSnapshot, error)
	ApplyDailyIntent(context.Context, MenuIntent, time.Time) (bool, error)
}
type IntentRepository interface {
	CancelDailyActorInputs(context.Context, int64, int64) ([]int, error)
	CreateDailyIntent(context.Context, MenuIntent, time.Time) error
	DailyIntent(context.Context, string, int64, int, time.Time) (MenuIntent, error)
	BindDailyPrompt(context.Context, string, int) error
	DailyReplyIntent(context.Context, int64, int64, int, time.Time) (MenuIntent, error)
	CancelDailyIntents(context.Context, int64, int, time.Time) ([]int, error)
}
type PreviewMenuRepository interface {
	ClaimDailyPreview(context.Context, MenuIntent, time.Time) error
	FinishDailyPreview(context.Context, MenuIntent, string, int, time.Time) (bool, error)
}
type PanelRepository interface {
	CloseDailyPanel(context.Context, int64, int) error
	DailyPanelClosed(context.Context, int64, int) (bool, error)
}
type MenuRepository interface {
	PanelRepository
	ConfigMenuRepository
	IntentRepository
	PreviewMenuRepository
}

// MenuService deliberately cannot call a foreground provider.
type MenuService struct {
	Store MenuRepository
	Stock BackgroundRepository
	Log   *slog.Logger
}

func (s *MenuService) NewIntent(ctx context.Context, i MenuIntent, now time.Time) (MenuIntent, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return i, err
	}
	i.Token = hex.EncodeToString(b[:])
	i.Expires = now.Add(10 * time.Minute)
	i.State = "pending"
	return i, s.Store.CreateDailyIntent(ctx, i, now)
}
func (s *MenuService) Apply(ctx context.Context, i MenuIntent, now time.Time) (bool, error) {
	changed, err := s.Store.ApplyDailyIntent(ctx, i, now)
	if err == nil {
		s.Log.InfoContext(ctx, "daily menu action applied", "chat_id", i.ChatID, "kind", i.Kind, "action", i.Action, "changed", changed)
	} else {
		s.Log.DebugContext(ctx, "daily menu action declined", "chat_id", i.ChatID, "kind", i.Kind)
	}
	return changed, err
}
func (s *MenuService) ReadyPreview(ctx context.Context, i MenuIntent, now time.Time) (Item, bool, error) {
	item, found, err := s.Stock.PreviewAIItem(ctx, i.ChatID, i.Kind, now)
	if err != nil {
		return item, false, err
	}
	if !found {
		err = s.Stock.RequestAIRefill(ctx, i.ChatID, i.Kind, now)
	}
	return item, found, err
}

package daily

import (
	"context"
	"errors"
	"github.com/vaporon4a/movie-helper/internal/interaction"
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
type MenuIntent = interaction.Intent
type ConfigMenuRepository interface {
	DailyConfig(context.Context, int64) (ConfigSnapshot, error)
	ApplyDailyIntent(context.Context, MenuIntent, time.Time) (bool, error)
}
type IntentRepository = interaction.IntentRepository
type PreviewIssue struct {
	Token, Kind, State string
	Revision           int64
}
type PreviewMenuRepository interface {
	PreviewIssue(context.Context, int64, string, time.Time) (PreviewIssue, bool, error)
	ResolvePreview(context.Context, int64, PreviewIssue, bool, time.Time) error
	ClaimDailyPreview(context.Context, MenuIntent, time.Time) error
	FinishDailyPreview(context.Context, MenuIntent, string, int, time.Time) (bool, error)
}
type PanelRepository = interaction.PanelRepository
type MenuRepository interface {
	ConfigMenuRepository
	PreviewMenuRepository
}

// MenuService deliberately cannot call a foreground provider.
type MenuService struct {
	store MenuRepository
	Stock BackgroundRepository
	Log   *slog.Logger
}

func NewMenuService(store MenuRepository, stock BackgroundRepository, log *slog.Logger) *MenuService {
	return &MenuService{store: store, Stock: stock, Log: log}
}
func (s *MenuService) Config(ctx context.Context, chat int64) (ConfigSnapshot, error) {
	return s.store.DailyConfig(ctx, chat)
}
func (s *MenuService) Claim(ctx context.Context, i MenuIntent, now time.Time) error {
	return s.store.ClaimDailyPreview(ctx, i, now)
}
func (s *MenuService) Finish(ctx context.Context, i MenuIntent, state string, message int, now time.Time) (bool, error) {
	return s.store.FinishDailyPreview(ctx, i, state, message, now)
}
func (s *MenuService) Apply(ctx context.Context, i MenuIntent, now time.Time) (bool, error) {
	changed, err := s.store.ApplyDailyIntent(ctx, i, now)
	switch {
	case err == nil:
		s.Log.InfoContext(ctx, "daily menu action applied", "chat_id", i.ChatID, "kind", i.Kind, "action", i.Action, "changed", changed)
	case errors.Is(err, ErrConflict):
		s.Log.DebugContext(ctx, "daily menu action declined", "chat_id", i.ChatID, "kind", i.Kind)
	default:
		s.Log.WarnContext(ctx, "daily menu storage failed", "chat_id", i.ChatID, "operation_id", i.Token, "reason", "storage_unavailable")
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

func (s *MenuService) PreviewIssue(ctx context.Context, chat int64, kind string, now time.Time) (PreviewIssue, bool, error) {
	return s.store.PreviewIssue(ctx, chat, kind, now)
}
func (s *MenuService) ResolvePreview(ctx context.Context, chat int64, issue PreviewIssue, sent bool, now time.Time) error {
	err := s.store.ResolvePreview(ctx, chat, issue, sent, now)
	if err == nil {
		s.Log.InfoContext(ctx, "preview explicitly resolved", "chat_id", chat, "kind", issue.Kind, "operation_id", issue.Token, "revision", issue.Revision, "sent", sent)
	}
	return err
}

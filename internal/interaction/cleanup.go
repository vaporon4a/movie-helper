package interaction

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

type Cleanup struct {
	RetryAfter time.Duration
	ChatID     int64
	MessageID  int
	Attempts   int
}
type CleanupError struct{ After time.Duration }

func (e *CleanupError) Error() string { return "UI cleanup rate limited" }

type CleanupRepository interface {
	ExpireInteractions(context.Context, time.Time) error
	ClaimUICleanup(context.Context, time.Time) (Cleanup, bool, error)
	FinishUICleanup(context.Context, Cleanup, bool, time.Time) error
}
type PanelRemover interface {
	RemoveUIPanel(context.Context, int64, int) error
}
type Maintenance struct {
	Store   CleanupRepository
	Remover PanelRemover
	Log     *slog.Logger
}

func (w Maintenance) Tick(ctx context.Context, now time.Time) error {
	if err := w.Store.ExpireInteractions(ctx, now); err != nil {
		return err
	}
	for range 10 {
		job, found, err := w.Store.ClaimUICleanup(ctx, now)
		if err != nil || !found {
			return err
		}
		attempt, cancel := context.WithTimeout(ctx, 15*time.Second)
		err = w.Remover.RemoveUIPanel(attempt, job.ChatID, job.MessageID)
		cancel()
		if rate, ok := errors.AsType[*CleanupError](err); ok {
			job.RetryAfter = rate.After
		}
		if e := w.Store.FinishUICleanup(ctx, job, err == nil, now); e != nil {
			return e
		}
		if err != nil {
			w.Log.WarnContext(ctx, "UI cleanup deferred", "chat_id", job.ChatID, "message_id", job.MessageID, "reason", "telegram_unavailable")
		} else {
			w.Log.DebugContext(ctx, "UI message removed", "chat_id", job.ChatID, "message_id", job.MessageID)
		}
	}
	return nil
}
func (w Maintenance) Run(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		if err := w.Tick(ctx, time.Now()); err != nil {
			w.Log.WarnContext(ctx, "UI maintenance failed", "reason", "storage_unavailable")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

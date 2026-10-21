package clubprofile

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

type CleanupRepository interface {
	DueProfileCleanup(context.Context, int64, time.Time) ([]CleanupJob, error)
	ClaimProfileCleanup(context.Context, int64, time.Time) (bool, error)
	SaveProfileCleanup(context.Context, int64, CleanupState, time.Time, string) error
}

type PollDeleter interface {
	DeleteProfilePoll(context.Context, CleanupJob) error
}

type CleanupWorker struct {
	Store     CleanupRepository
	Transport PollDeleter
	Allowed   map[int64]bool
	Log       *slog.Logger
	Now       func() time.Time
}

func (w CleanupWorker) Tick(ctx context.Context) error {
	var errs []error
	for chat, allowed := range w.Allowed {
		if !allowed {
			continue
		}
		jobs, err := w.Store.DueProfileCleanup(ctx, chat, w.Now())
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, j := range jobs {
			if err = w.cleanup(ctx, j); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func (w CleanupWorker) cleanup(ctx context.Context, j CleanupJob) error {
	now := w.Now()
	if !now.Before(j.Deadline) {
		return w.Store.SaveProfileCleanup(ctx, j.PollID, CleanupExpired, now, "deadline")
	}
	claimed, err := w.Store.ClaimProfileCleanup(ctx, j.PollID, now)
	if err != nil || !claimed {
		return err
	}
	err = w.Transport.DeleteProfilePoll(ctx, j)
	state, reason, next := CleanupDeleted, "", now
	if err != nil {
		state, reason, next = CleanupPending, "transport_unknown", now.Add(5*time.Minute)
		if e, ok := errors.AsType[*DeliveryError](err); ok {
			reason = e.Reason
			if e.Kind == "permanent" || e.Kind == "forbidden" {
				state = CleanupBlocked
			}
			if e.After > 0 {
				next = now.Add(e.After)
			}
		}
	}
	persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	w.Log.InfoContext(ctx, "profile poll cleanup", "poll_id", j.PollID, "state", state, "reason", reason)
	return w.Store.SaveProfileCleanup(persist, j.PollID, state, next, reason)
}

func (w CleanupWorker) Run(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		if err := w.Tick(ctx); err != nil && !errors.Is(err, context.Canceled) {
			w.Log.WarnContext(ctx, "profile cleanup cycle failed", "reason", "storage")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

package aiwork

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

const defaultLease = 6 * time.Minute

type Repository interface {
	SeedAIWork(context.Context, time.Time) error
	ClaimAIWork(context.Context, time.Time, time.Duration) (Work, bool, error)
	DeferAIWork(context.Context, int64, time.Time, string) error
	CompleteAIWork(context.Context, int64, time.Time) error
}

type Executor interface {
	Execute(context.Context, Work, time.Time) (Outcome, error)
}

type Coordinator struct {
	Store     Repository
	Executors map[string]Executor
	Log       *slog.Logger
	Now       func() time.Time
	Interval  time.Duration
	Lease     time.Duration
}

func NewCoordinator(store Repository, executors map[string]Executor, log *slog.Logger, now func() time.Time) (*Coordinator, error) {
	if store == nil || len(executors) == 0 {
		return nil, errors.New("AI work coordinator dependencies are required")
	}
	if log == nil {
		log = slog.Default()
	}
	if now == nil {
		now = time.Now
	}
	return &Coordinator{Store: store, Executors: executors, Log: log, Now: now, Interval: 15 * time.Second, Lease: defaultLease}, nil
}

func (c *Coordinator) Run(ctx context.Context) {
	interval := c.Interval
	if interval <= 0 {
		interval = 15 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := c.Tick(ctx); err != nil && ctx.Err() == nil {
			c.Log.Error("AI work tick failed", "reason", "internal")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (c *Coordinator) Tick(ctx context.Context) error {
	now := c.Now()
	if err := c.Store.SeedAIWork(ctx, now); err != nil {
		return err
	}
	lease := c.Lease
	if lease <= 0 {
		lease = defaultLease
	}
	work, claimed, err := c.Store.ClaimAIWork(ctx, now, lease)
	if err != nil || !claimed {
		return err
	}
	c.Log.Info("AI work claimed", "work_id", work.ID, "kind", work.Kind, "scope_id", work.ScopeID, "attempt", work.Attempts)
	executor := c.Executors[work.Kind]
	if executor == nil {
		return c.deferWork(ctx, work, now.Add(24*time.Hour), "executor_unavailable")
	}
	started := time.Now()
	outcome, executeErr := executor.Execute(ctx, work, now)
	if executeErr != nil {
		after := retryDelay(work.Attempts)
		c.Log.Warn("AI work execution failed", "work_id", work.ID, "kind", work.Kind, "scope_id", work.ScopeID, "reason", "internal", "retry_after_ms", after.Milliseconds())
		return c.deferWork(ctx, work, now.Add(after), "internal")
	}
	if outcome.Done {
		if err = c.Store.CompleteAIWork(context.WithoutCancel(ctx), work.ID, c.Now()); err != nil {
			return err
		}
		c.Log.Info("AI work completed", "work_id", work.ID, "kind", work.Kind, "scope_id", work.ScopeID, "duration_ms", time.Since(started).Milliseconds())
		return nil
	}
	after := outcome.After
	if after <= 0 {
		after = retryDelay(work.Attempts)
	}
	reason := outcome.Reason
	if reason == "" {
		reason = "retry"
	}
	return c.deferWork(ctx, work, now.Add(after), reason)
}

func (c *Coordinator) deferWork(ctx context.Context, work Work, next time.Time, reason string) error {
	persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := c.Store.DeferAIWork(persist, work.ID, next, reason); err != nil {
		return err
	}
	c.Log.Info("AI work deferred", "work_id", work.ID, "kind", work.Kind, "scope_id", work.ScopeID, "reason", reason, "next_attempt", next)
	return nil
}

func retryDelay(attempt int) time.Duration {
	delays := []time.Duration{5 * time.Minute, 15 * time.Minute, 30 * time.Minute, time.Hour, 2 * time.Hour}
	if attempt < 1 {
		attempt = 1
	}
	if attempt > len(delays) {
		return delays[len(delays)-1]
	}
	return delays[attempt-1]
}

package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/vaporon4a/movie-helper/internal/daily"
)

type Sender interface {
	Send(context.Context, int64, daily.Item) (int, error)
}
type Provider interface {
	Candidates(context.Context, string, int64) ([]daily.Item, error)
}
type Repository interface {
	Schedules(context.Context, int64) ([]daily.Schedule, error)
	Reserve(context.Context, daily.Schedule, string, int64, int64) (int64, error)
	Preparing(context.Context) ([]daily.Delivery, error)
	ClaimPreparation(context.Context, int64, time.Time) (bool, error)
	DeferPreparation(context.Context, int64, time.Time) error
	HasApproved(context.Context, int64, string) (bool, error)
	Seen(context.Context, int64, string, string) (bool, error)
	Attach(context.Context, int64, *daily.Item, time.Time) error
	Pending(context.Context) ([]daily.Delivery, error)
	Claim(context.Context, int64, time.Time) (bool, error)
	Finish(context.Context, int64, string, int, int64) error
	Suspend(context.Context, int64) error
}
type Scheduler struct {
	Store    Repository
	Sender   Sender
	Provider Provider
	Allowed  map[int64]bool
	Log      *slog.Logger
	Now      func() time.Time
}

func New(store Repository, sender Sender, provider Provider, allowed map[int64]bool, log *slog.Logger, now func() time.Time) (*Scheduler, error) {
	if store == nil || sender == nil {
		return nil, errors.New("daily scheduler dependencies are required")
	}
	if log == nil {
		log = slog.Default()
	}
	if now == nil {
		now = time.Now
	}
	return &Scheduler{Store: store, Sender: sender, Provider: provider, Allowed: allowed, Log: log, Now: now}, nil
}

// Slot walks the local calendar day in absolute minutes. The first occurrence
// wins during a repeated hour; a DST gap advances to the next existing minute.
func Slot(now time.Time, clock, zone string) (time.Time, error) {
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return time.Time{}, err
	}
	parsed, err := time.Parse("15:04", clock)
	if err != nil {
		return time.Time{}, err
	}
	local := now.In(loc)
	y, m, d := local.Date()
	anchor := time.Date(y, m, d, 12, 0, 0, 0, loc)
	want := parsed.Hour()*60 + parsed.Minute()
	for t := anchor.Add(-18 * time.Hour); !t.After(anchor.Add(18 * time.Hour)); t = t.Add(time.Minute) {
		l := t.In(loc)
		ly, lm, ld := l.Date()
		if ly == y && lm == m && ld == d && l.Hour()*60+l.Minute() >= want {
			return t, nil
		}
	}
	return time.Time{}, errors.New("no slot on local day")
}
func (s *Scheduler) Run(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		if err := s.Tick(ctx); err != nil && ctx.Err() == nil {
			s.Log.Error("scheduler tick failed")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (s *Scheduler) Tick(ctx context.Context) error {
	now := s.Now()
	if err := s.reserveDue(ctx, now); err != nil {
		return err
	}
	if err := s.prepareDue(ctx); err != nil {
		return err
	}
	return s.deliverDue(ctx)
}

func (s *Scheduler) reserveDue(ctx context.Context, now time.Time) error {
	schedules, err := s.Store.Schedules(ctx, 0)
	if err != nil {
		return err
	}
	for _, sc := range schedules {
		if !s.Allowed[sc.ChatID] || !sc.Enabled || sc.Zone == "" {
			continue
		}
		slot, e := Slot(now, sc.Clock, sc.Zone)
		if e != nil {
			return e
		}
		deadline := preparationDeadline(slot)
		if now.Before(slot) || !now.Before(deadline) || slot.Unix() <= sc.Effective {
			continue
		}
		if _, e = s.Store.Reserve(ctx, sc, slot.Format("2006-01-02"), slot.Unix(), deadline.Unix()); e != nil {
			return e
		}
	}
	return nil
}

func (s *Scheduler) prepareDue(ctx context.Context) error {
	preparing, err := s.Store.Preparing(ctx)
	if err != nil {
		return err
	}
	for _, delivery := range preparing {
		if err = s.prepareIfDue(ctx, delivery); err != nil {
			return err
		}
	}
	return nil
}

func (s *Scheduler) prepareIfDue(ctx context.Context, delivery daily.Delivery) error {
	now := s.Now()
	if !s.Allowed[delivery.ChatID] || now.Unix() >= delivery.Deadline || delivery.FetchAttempts >= daily.MaxPreparationAttempts {
		return s.Store.DeferPreparation(ctx, delivery.ID, time.Time{})
	}
	if now.Unix() < delivery.NextAttempt {
		return nil
	}
	claimed, err := s.Store.ClaimPreparation(ctx, delivery.ID, now)
	if err != nil || !claimed {
		return err
	}
	return s.prepare(ctx, delivery)
}

func (s *Scheduler) deliverDue(ctx context.Context) error {
	pending, err := s.Store.Pending(ctx)
	if err != nil {
		return err
	}
	for _, delivery := range pending {
		if err = s.deliverIfDue(ctx, delivery); err != nil {
			return err
		}
	}
	return nil
}

func (s *Scheduler) deliverIfDue(ctx context.Context, delivery daily.Delivery) error {
	now := s.Now()
	if !s.Allowed[delivery.ChatID] || now.Unix() >= delivery.Deadline {
		err := s.Store.Finish(ctx, delivery.ID, "cancelled", 0, 0)
		if errors.Is(err, daily.ErrConflict) {
			return nil
		}
		return err
	}
	if now.Unix() < delivery.NextAttempt {
		return nil
	}
	claimed, err := s.Store.Claim(ctx, delivery.ID, now)
	if err != nil || !claimed {
		return err
	}
	return s.deliver(ctx, delivery)
}

func (s *Scheduler) deliver(ctx context.Context, delivery daily.Delivery) error {
	message, sendErr := s.Sender.Send(ctx, delivery.ChatID, delivery.Item)
	state, next, forbidden := s.deliveryResult(delivery, sendErr)
	if sendErr != nil {
		s.Log.Warn("delivery not confirmed", "delivery_id", delivery.ID, "state", state)
	}
	// Persist even when SIGTERM cancelled the network request.
	persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := s.Store.Finish(persist, delivery.ID, state, message, next); err != nil {
		return err
	}
	if forbidden {
		return s.Store.Suspend(persist, delivery.ChatID)
	}
	return nil
}

func (s *Scheduler) deliveryResult(delivery daily.Delivery, sendErr error) (string, int64, bool) {
	if sendErr == nil {
		return "sent", 0, false
	}
	typed, ok := errors.AsType[*daily.SendError](sendErr)
	if !ok {
		return "unknown", 0, false
	}
	switch typed.Kind {
	case "retry":
		next := s.Now().Add(max(typed.After, time.Second)).Unix()
		if next >= delivery.Deadline {
			return "cancelled", next, false
		}
		return "retry", next, false
	case "permanent":
		return "failed", 0, false
	case "forbidden":
		return "cancelled", 0, true
	default:
		return "unknown", 0, false
	}
}

// Never publish a delayed daily item on the next local calendar day.
func preparationDeadline(slot time.Time) time.Time {
	y, m, d := slot.Date()
	midnight := time.Date(y, m, d+1, 0, 0, 0, 0, slot.Location())
	end := slot.Add(6 * time.Hour)
	if midnight.Before(end) {
		end = midnight
	}
	return end
}

func preparationDelay(attempt int) time.Duration {
	delays := []time.Duration{5 * time.Minute, 15 * time.Minute, 30 * time.Minute, time.Hour, 2 * time.Hour}
	if attempt < 1 || attempt > len(delays) {
		return 0
	}
	return delays[attempt-1]
}

func (s *Scheduler) prepare(ctx context.Context, d daily.Delivery) (err error) {
	finished := false
	defer func() {
		if finished {
			return
		}
		if deferErr := s.deferPreparation(ctx, d); deferErr != nil {
			err = deferErr
		}
	}()
	candidate, approved, err := s.preparationCandidate(ctx, d)
	if err != nil {
		return err
	}
	if !approved && candidate == nil {
		return nil
	}
	if e := s.Store.Attach(ctx, d.ID, candidate, s.Now()); e != nil {
		s.Log.Warn("slot preparation stopped", "delivery_id", d.ID)
		return nil
	}
	finished = true
	return nil
}

func (s *Scheduler) deferPreparation(ctx context.Context, delivery daily.Delivery) error {
	delay := preparationDelay(delivery.FetchAttempts + 1)
	next := s.Now().Add(delay)
	if delay == 0 || next.Unix() >= delivery.Deadline {
		next = time.Time{}
	}
	persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := s.Store.DeferPreparation(persist, delivery.ID, next); err != nil {
		return err
	}
	s.Log.Info("content preparation deferred", "delivery_id", delivery.ID, "attempt", delivery.FetchAttempts+1, "retry", !next.IsZero(), "next_attempt", next)
	return nil
}

func (s *Scheduler) preparationCandidate(ctx context.Context, delivery daily.Delivery) (*daily.Item, bool, error) {
	approved, err := s.Store.HasApproved(ctx, delivery.ChatID, delivery.Kind)
	if err != nil || approved || s.Provider == nil {
		return nil, approved, err
	}
	fetchCtx, cancel := context.WithTimeout(ctx, daily.FetchTimeout)
	candidates, err := s.Provider.Candidates(fetchCtx, delivery.Kind, delivery.ChatID)
	cancel()
	if err != nil {
		s.Log.Warn("content source unavailable", "chat_id", delivery.ChatID, "kind", delivery.Kind)
		return nil, false, nil
	}
	for _, candidate := range candidates {
		seen, seenErr := s.Store.Seen(ctx, delivery.ChatID, delivery.Kind, candidate.Key)
		if seenErr != nil {
			return nil, false, seenErr
		}
		if !seen {
			return &candidate, false, nil
		}
	}
	return nil, false, nil
}

package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/vaporon4a/movie-helper/internal/content"
	"github.com/vaporon4a/movie-helper/internal/daily"
	"github.com/vaporon4a/movie-helper/internal/storage"
)

type Sender interface {
	Send(context.Context, int64, daily.Item) (int, error)
}
type Provider interface {
	Candidates(context.Context, string, int64) ([]daily.Item, error)
}
type Scheduler struct {
	Store    *storage.Store
	Sender   Sender
	Provider Provider
	Allowed  map[int64]bool
	Log      *slog.Logger
	Now      func() time.Time
	mu       sync.Mutex
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
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.Now()
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
		if now.Before(slot) || !now.Before(slot.Add(time.Hour)) || slot.Unix() <= sc.Effective {
			continue
		}
		id, e := s.Store.Reserve(ctx, sc, slot.Format("2006-01-02"), slot.Add(time.Hour).Unix())
		if e != nil {
			return e
		}
		if id == 0 {
			continue
		}
		var candidate *daily.Item
		approved, e := s.Store.HasApproved(ctx, sc.ChatID, sc.Kind)
		if e != nil {
			return e
		}
		if !approved && s.Provider != nil {
			fetchCtx, cancel := context.WithTimeout(ctx, content.FetchTimeout)
			candidates, e := s.Provider.Candidates(fetchCtx, sc.Kind, sc.ChatID)
			cancel()
			if e != nil {
				s.Log.Warn("content source unavailable", "chat_id", sc.ChatID)
			}
			for _, i := range candidates {
				seen, e := s.Store.Seen(ctx, sc.ChatID, sc.Kind, i.Key)
				if e != nil {
					return e
				}
				if !seen {
					candidate = &i
					break
				}
			}
		}
		if e = s.Store.Attach(ctx, id, candidate, s.Now()); e != nil {
			// A settings change may have cancelled this slot during the network fetch.
			s.Log.Warn("slot preparation stopped", "delivery_id", id)
		}
	}
	pending, err := s.Store.Pending(ctx)
	if err != nil {
		return err
	}
	for _, d := range pending {
		now = s.Now()
		if !s.Allowed[d.ChatID] || now.Unix() >= d.Deadline {
			if err = s.Store.Finish(ctx, d.ID, "cancelled", 0, 0); err != nil && !errors.Is(err, daily.ErrConflict) {
				return err
			}
			continue
		}
		if now.Unix() < d.NextAttempt {
			continue
		}
		claimed, e := s.Store.Claim(ctx, d.ID, now)
		if e != nil {
			return e
		}
		if !claimed {
			continue
		}
		message, sendErr := s.Sender.Send(ctx, d.ChatID, d.Item)
		state := "sent"
		var next int64
		forbidden := false
		if sendErr != nil {
			state = "unknown"
			var typed *daily.SendError
			if errors.As(sendErr, &typed) {
				switch typed.Kind {
				case "retry":
					state = "retry"
					delay := typed.After
					if delay < time.Second {
						delay = time.Second
					}
					next = s.Now().Add(delay).Unix()
					if next >= d.Deadline {
						state = "cancelled"
					}
				case "permanent":
					state = "failed"
				case "forbidden":
					state = "cancelled"
					forbidden = true
				}
			}
			s.Log.Warn("delivery not confirmed", "delivery_id", d.ID, "state", state)
		}
		// Persist even when SIGTERM cancelled the network request.
		persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		err = s.Store.Finish(persist, d.ID, state, message, next)
		if err == nil && forbidden {
			err = s.Store.Suspend(persist, d.ChatID)
		}
		cancel()
		if err != nil {
			return err
		}
	}
	return nil
}

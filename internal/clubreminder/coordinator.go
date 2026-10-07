package clubreminder

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/vaporon4a/movie-helper/internal/daily"
)

type DeliveryRepository interface {
	ClubSchedules(context.Context) ([]Settings, error)
	ReserveClub(context.Context, Settings, Slot) error
	ClubDue(context.Context, time.Time) ([]Delivery, error)
	ClubFallbackCount(context.Context, int64, string) (int, error)
	ClaimClub(context.Context, int64, time.Time, Text) (Delivery, bool, error)
	FinishClub(context.Context, Delivery, string, int, time.Time, string, time.Time) error
}

type Sender interface {
	SendReminder(context.Context, int64, string) (int, error)
}
type Coordinator struct {
	Store   DeliveryRepository
	Sender  Sender
	Allowed map[int64]bool
	Log     *slog.Logger
	Now     func() time.Time
}

func (c *Coordinator) Run(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		if err := c.Tick(ctx); err != nil {
			c.Log.Error("club reminder tick failed", "reason", "storage")
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
	schedules, err := c.Store.ClubSchedules(ctx)
	if err != nil {
		return err
	}
	for _, settings := range schedules {
		if !c.Allowed[settings.ChatID] {
			continue
		}
		c.reserveSchedule(ctx, settings, now)
	}

	due, err := c.Store.ClubDue(ctx, now)
	if err != nil {
		return err
	}
	for _, delivery := range due {
		if !c.Allowed[delivery.ChatID] {
			continue
		}
		if err = c.deliver(ctx, delivery, now); err != nil {
			c.Log.Error("club reminder processing failed", "delivery_id", delivery.ID, "reason", "storage")
		}
	}
	return nil
}
func (c *Coordinator) reserveSchedule(ctx context.Context, settings Settings, now time.Time) {
	slots, err := Slots(settings, now)
	if err != nil {
		c.Log.Warn("club reminder invalid schedule", "chat_id", settings.ChatID)
		return
	}
	for _, slot := range slots {
		if slot.At.Unix() <= settings.Effective || now.Before(slot.At) || !now.Before(slot.At.Add(DeliveryWindow)) {
			continue
		}
		if err = c.Store.ReserveClub(ctx, settings, slot); err != nil {
			c.Log.Error("club reminder reserve failed", "chat_id", settings.ChatID, "reason", "storage")
		}
	}
}
func (c *Coordinator) deliver(ctx context.Context, delivery Delivery, now time.Time) error {
	uses, err := c.Store.ClubFallbackCount(ctx, delivery.ChatID, delivery.Intent)
	if err != nil {
		return err
	}
	fallback := FallbackForUsage(delivery.Intent, uses)
	d, claimed, err := c.Store.ClaimClub(ctx, delivery.ID, now, fallback)
	if err != nil || !claimed {
		return err
	}
	attempt, cancel := context.WithTimeout(ctx, 20*time.Second)
	message, sendErr := c.Sender.SendReminder(attempt, d.ChatID, d.Text.Body)
	cancel()
	state, code, next := Sent, "", time.Time{}
	if sendErr != nil {
		state, code = Unknown, "telegram_unknown"
		if problem, ok := errors.AsType[*daily.SendError](sendErr); ok {
			switch problem.Kind {
			case "retry":
				state, code, next = Retry, "telegram_rate_limit", c.Now().Add(max(problem.After, time.Second))
			case "forbidden":
				state, code = "failed", "telegram_forbidden"
			case "permanent":
				state, code = "failed", "telegram_rejected"
			}
		}
	}
	persist, done := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer done()
	err = c.Store.FinishClub(persist, d, state, message, next, code, c.Now())
	c.Log.Info("club reminder delivery finished", "chat_id", d.ChatID, "delivery_id", d.ID, "state", state, "tone", d.Text.Tone, "source", d.Text.Provider, "reason", code)
	return err
}
func RandomTone() string {
	// #nosec G404 -- creative tone rotation has no security significance.
	return Tones[rand.IntN(len(Tones))]
}

package clubprofile

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/vaporon4a/movie-helper/internal/daily"
)

type StatisticsDelivery struct {
	ID, ChatID int64
	Text       string
}
type StatisticsDeliveryRepository interface {
	StatisticsSchedules(context.Context) ([]StatisticsSettings, error)
	ReserveStatistics(context.Context, StatisticsSettings, StatisticsSnapshot, time.Time) error
	StatisticsDue(context.Context, time.Time) ([]StatisticsDelivery, error)
	ClaimStatistics(context.Context, int64, time.Time) (bool, error)
	FinishStatistics(context.Context, int64, string, int, time.Time, string) error
}
type StatisticsSender interface {
	SendReminder(context.Context, int64, string) (int, error)
}
type StatisticsCoordinator struct {
	Store   StatisticsDeliveryRepository
	Reader  StatisticsReader
	Sender  StatisticsSender
	Allowed map[int64]bool
	Now     func() time.Time
	Log     *slog.Logger
}

func (c *StatisticsCoordinator) Run(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		if err := c.Tick(ctx); err != nil {
			c.Log.Error("club statistics tick failed", "reason", "storage")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (c *StatisticsCoordinator) Tick(ctx context.Context) error {
	now := c.Now()
	schedules, err := c.Store.StatisticsSchedules(ctx)
	if err != nil {
		return err
	}
	for _, sc := range schedules {
		if !c.Allowed[sc.ChatID] {
			continue
		}
		if err = c.reserve(ctx, sc, now); err != nil {
			c.Log.Warn("club statistics reservation failed", "chat_id", sc.ChatID, "reason", "snapshot_or_schedule")
		}
	}
	due, err := c.Store.StatisticsDue(ctx, now)
	if err != nil {
		return err
	}
	for _, d := range due {
		if !c.Allowed[d.ChatID] {
			continue
		}
		if err = c.deliver(ctx, d, now); err != nil {
			c.Log.Error("club statistics receipt failed", "delivery_id", d.ID, "reason", "storage")
		}
	}
	return nil
}
func (c *StatisticsCoordinator) reserve(ctx context.Context, sc StatisticsSettings, now time.Time) error {
	slot, err := DigestSlot(sc, now)
	if err != nil {
		return err
	}
	if !slot.After(sc.Effective) || now.Before(slot) || !now.Before(slot.Add(24*time.Hour)) {
		return nil
	}
	in, err := c.Reader.StatisticsInput(ctx, sc.ChatID)
	if err != nil {
		return err
	}
	all := BuildStatistics(in, StatisticsPeriod{}, now)
	report := BuildStatistics(in, DigestPeriod(slot), now)
	claims := make([]StatisticsClaim, 0, len(all.Claims))
	for _, claim := range all.Claims {
		inPeriod := report.Period.Contains(claim.ClosedAt)
		late := claim.ClosedAt.Before(report.Period.Start) && !claim.ReceivedAt.Before(report.Period.Start)
		if inPeriod || late {
			claims = append(claims, claim)
		}
	}
	report.Claims = claims
	return c.Store.ReserveStatistics(ctx, sc, report, slot)
}
func (c *StatisticsCoordinator) deliver(ctx context.Context, d StatisticsDelivery, now time.Time) error {
	claimed, err := c.Store.ClaimStatistics(ctx, d.ID, now)
	if err != nil || !claimed {
		return err
	}
	attempt, cancel := context.WithTimeout(ctx, 20*time.Second)
	message, sendErr := c.Sender.SendReminder(attempt, d.ChatID, d.Text)
	cancel()
	state, reason, next := "sent", "", time.Time{}
	if sendErr != nil {
		state, reason = "unknown", "telegram_unknown"
		if p, ok := errors.AsType[*daily.SendError](sendErr); ok {
			switch p.Kind {
			case "retry":
				state, reason, next = "retry", "telegram_rate_limit", c.Now().Add(max(p.After, time.Second))
			case "forbidden":
				state, reason = "failed", "telegram_forbidden"
			case "permanent":
				state, reason = "failed", "telegram_rejected"
			}
		}
	}
	persist, done := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer done()
	err = c.Store.FinishStatistics(persist, d.ID, state, message, next, reason)
	c.Log.Info("club statistics delivery finished", "chat_id", d.ChatID, "delivery_id", d.ID, "state", state, "reason", reason)
	return err
}

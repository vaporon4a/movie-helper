package featurevote

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

const scheduleCatchup = 6 * time.Hour
const titlePreparationTimeout = 90 * time.Second

type Coordinator struct {
	store   CoordinatorRepository
	sender  Transport
	titles  TitleGenerator
	allowed map[int64]bool
	log     *slog.Logger
	now     func() time.Time
	token   func() (string, error)
}

func NewCoordinator(store CoordinatorRepository, sender Transport, titles TitleGenerator, allowed map[int64]bool, log *slog.Logger, now func() time.Time, token func() (string, error)) (*Coordinator, error) {
	if store == nil || sender == nil || titles == nil || token == nil {
		return nil, errors.New("feature vote coordinator dependencies are required")
	}
	if log == nil {
		log = slog.Default()
	}
	if now == nil {
		now = time.Now
	}
	return &Coordinator{store: store, sender: sender, titles: titles, allowed: allowed, log: log, now: now, token: token}, nil
}

func (c *Coordinator) Run(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		if err := c.Tick(ctx); err != nil && ctx.Err() == nil {
			c.log.Error("feature vote tick failed", "reason", "internal")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (c *Coordinator) Tick(ctx context.Context) error {
	now := c.now()
	for _, step := range []func(context.Context, time.Time) error{c.reserveSchedules, c.openPlanned, c.closeDue, c.publishReady} {
		if err := step(ctx, now); err != nil {
			return err
		}
	}
	return nil
}

func (c *Coordinator) reserveSchedules(ctx context.Context, now time.Time) error {
	schedules, err := c.store.FeatureSchedules(ctx, 0)
	if err != nil {
		return err
	}
	for _, schedule := range schedules {
		if err = c.reserveSchedule(ctx, schedule, now); err != nil {
			return err
		}
	}
	return nil
}

func (c *Coordinator) reserveSchedule(ctx context.Context, schedule Schedule, now time.Time) error {
	if !schedule.Enabled || !c.allowed[schedule.ChatID] || schedule.Zone == "" {
		return nil
	}
	slot, err := WeeklySlot(now, schedule.Weekday, schedule.Clock, schedule.Zone)
	if err != nil {
		return err
	}
	if now.Before(slot) || !now.Before(slot.Add(scheduleCatchup)) || slot.Unix() <= schedule.Effective {
		return nil
	}
	token, err := c.token()
	if err != nil {
		return err
	}
	_, err = c.store.ReserveFeatureRound(ctx, schedule.ChatID, slot.Unix(), slot.Add(DefaultRoundDuration).Unix(), token)
	if errors.Is(err, ErrDuplicate) || errors.Is(err, ErrActiveRound) {
		return nil
	}
	return err
}

func (c *Coordinator) openPlanned(ctx context.Context, now time.Time) error {
	rounds, err := c.store.FeatureRounds(ctx, RoundPlanned)
	if err != nil {
		return err
	}
	for _, round := range rounds {
		if !c.allowed[round.ChatID] || round.NextAttempt > now.Unix() {
			continue
		}
		if err = c.openRound(ctx, round, now); err != nil {
			return err
		}
	}
	return nil
}

func (c *Coordinator) openRound(ctx context.Context, round Round, now time.Time) error {
	prepareCtx, cancel := context.WithTimeout(ctx, titlePreparationTimeout)
	defer cancel()
	options, err := c.prepareOptions(prepareCtx, round)
	if err != nil {
		return err
	}
	if len(options) == 0 {
		return c.store.CancelEmptyFeatureRound(ctx, round.ID)
	}
	claimed, err := c.store.ClaimFeatureRound(ctx, round.ID, RoundPlanned, RoundOpening, now)
	if err != nil || !claimed {
		return err
	}
	round.Options = options
	opened, sendErr := c.sender.OpenRound(ctx, round, c.deepLink(round.Token))
	if sendErr != nil {
		return c.handleFailure(ctx, round, RoundOpening, sendErr, now)
	}
	if err = c.store.OpenFeatureRound(ctx, round.ID, opened, now); err != nil {
		return err
	}
	c.log.Info("feature vote opened", "round_id", round.ID, "chat_id", round.ChatID, "ideas", len(options), "runoff", round.ParentID != 0, "ballot_mode", opened.Mode)
	return nil
}

func (c *Coordinator) prepareOptions(ctx context.Context, round Round) ([]Option, error) {
	options, err := c.store.FeatureRoundOptions(ctx, round.ID)
	if err != nil || len(options) > 0 {
		return options, err
	}
	ideas, err := c.store.ActiveFeatures(ctx, round.ChatID)
	if err != nil {
		return nil, err
	}
	options = make([]Option, 0, len(ideas))
	used := make(map[string]bool, len(ideas))
	for position, idea := range ideas {
		title := strings.TrimSpace(idea.Title)
		if title == "" {
			title = c.titles.Title(ctx, idea.Text)
			if saveErr := c.store.SaveFeatureTitle(ctx, idea.ID, title); saveErr != nil {
				return nil, saveErr
			}
		}
		key := strings.ToLower(title)
		if used[key] {
			title = FallbackTitle(idea.Text)
			if used[strings.ToLower(title)] {
				title = fmt.Sprintf("%s · #%d", title, idea.ID)
			}
		}
		used[strings.ToLower(title)] = true
		options = append(options, Option{RoundID: round.ID, IdeaID: idea.ID, Position: position, Title: title, Text: idea.Text})
	}
	if err = c.store.SaveFeatureRoundOptions(ctx, round.ID, options); err != nil {
		return nil, err
	}
	return options, nil
}

func (c *Coordinator) closeDue(ctx context.Context, now time.Time) error {
	rounds, err := c.store.FeatureRounds(ctx, RoundOpen)
	if err != nil {
		return err
	}
	for _, round := range rounds {
		if round.ClosesAt > now.Unix() || round.NextAttempt > now.Unix() {
			continue
		}
		if err = c.closeRound(ctx, round, now); err != nil {
			return err
		}
	}
	return nil
}

func (c *Coordinator) closeRound(ctx context.Context, round Round, now time.Time) error {
	claimed, err := c.store.ClaimFeatureRound(ctx, round.ID, RoundOpen, RoundClosing, now)
	if err != nil || !claimed {
		return err
	}
	if round.BallotMode == BallotNative {
		round.Options, err = c.store.FeatureRoundOptions(ctx, round.ID)
		if err != nil {
			return err
		}
		votes, closeErr := c.sender.CloseRound(ctx, round)
		if closeErr != nil {
			return c.handleFailure(ctx, round, RoundClosing, closeErr, now)
		}
		if err = c.store.SaveFeaturePoll(ctx, round.ID, votes); err != nil {
			return err
		}
	}
	token, err := c.token()
	if err != nil {
		return err
	}
	_, err = c.store.FinalizeFeatureRound(ctx, round.ID, token, now)
	return err
}

func (c *Coordinator) publishReady(ctx context.Context, now time.Time) error {
	rounds, err := c.store.FeatureRounds(ctx, RoundReady)
	if err != nil {
		return err
	}
	for _, round := range rounds {
		if round.NextAttempt > now.Unix() {
			continue
		}
		claimed, claimErr := c.store.ClaimFeatureRound(ctx, round.ID, RoundReady, RoundPublishing, now)
		if claimErr != nil || !claimed {
			return claimErr
		}
		round.Options, err = c.store.FeatureRoundOptions(ctx, round.ID)
		if err != nil {
			return err
		}
		if sendErr := c.sender.SendResult(ctx, round); sendErr != nil {
			return c.handleFailure(ctx, round, RoundPublishing, sendErr, now)
		}
		if err = c.store.PublishFeatureRound(ctx, round.ID); err != nil {
			return err
		}
		c.log.Info("feature vote result published", "round_id", round.ID, "chat_id", round.ChatID, "outcome", round.Outcome)
	}
	return nil
}

func (c *Coordinator) deepLink(token string) string {
	return "ideas_" + token
}

func (c *Coordinator) handleFailure(ctx context.Context, round Round, from RoundState, err error, now time.Time) error {
	typed, ok := errors.AsType[*DeliveryError](err)
	target, code, next := RoundUnknown, "telegram_unknown", time.Time{}
	if ok {
		switch typed.Kind {
		case DeliveryRetry:
			target, code, next = retryState(from), "telegram_rate_limit", now.Add(max(typed.After, time.Second))
		case DeliveryForbidden:
			target, code = RoundFailed, "telegram_forbidden"
			if disableErr := c.store.DisableFeatureSchedule(ctx, round.ChatID); disableErr != nil {
				return disableErr
			}
		case DeliveryPermanent:
			target, code = RoundFailed, "telegram_rejected"
		}
	}
	c.log.Warn("feature vote delivery failed", "round_id", round.ID, "chat_id", round.ChatID, "from", from, "target", target, "reason", code)
	return c.store.DeferFeatureRound(context.WithoutCancel(ctx), round.ID, from, target, next, code)
}

func retryState(from RoundState) RoundState {
	if from == RoundOpening {
		return RoundPlanned
	}
	if from == RoundPublishing {
		return RoundReady
	}
	if from == RoundClosing {
		return RoundOpen
	}
	return from
}

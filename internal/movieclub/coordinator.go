package movieclub

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"
)

const (
	catchupWindow = 6 * time.Hour
	recentWindow  = 90 * 24 * time.Hour
)

type Coordinator struct {
	store     CoordinatorRepository
	telegram  Transport
	scenarios ScenarioSet
	allowed   map[int64]bool
	log       *slog.Logger
	now       func() time.Time
}

func NewCoordinator(store CoordinatorRepository, telegram Transport, scenarios ScenarioSet, allowed map[int64]bool, log *slog.Logger, now func() time.Time) (*Coordinator, error) {
	if store == nil || telegram == nil || len(scenarios) == 0 {
		return nil, errors.New("movieclub coordinator dependencies are required")
	}
	if log == nil {
		log = slog.Default()
	}
	if now == nil {
		now = time.Now
	}
	return &Coordinator{store: store, telegram: telegram, scenarios: scenarios, allowed: allowed, log: log, now: now}, nil
}

func (c *Coordinator) Run(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		if err := c.Tick(ctx); err != nil && ctx.Err() == nil {
			c.log.Error("movieclub tick failed", "reason", safeReason(err))
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
	steps := []func(context.Context, time.Time) error{
		c.reserveSchedules,
		c.openPlanned,
		c.closeDue,
		c.prepareSelections,
		c.publishReady,
	}
	for _, step := range steps {
		if err := step(ctx, now); err != nil {
			return err
		}
	}
	return nil
}

func (c *Coordinator) reserveSchedules(ctx context.Context, now time.Time) error {
	schedules, err := c.store.MovieSchedules(ctx, 0)
	if err != nil {
		return err
	}
	for _, schedule := range schedules {
		slot, eligible, slotErr := c.scheduleSlot(schedule, now)
		if slotErr != nil {
			return slotErr
		}
		if !eligible {
			continue
		}
		scenario := c.scenarios[schedule.Feature]
		if scenario == nil {
			return ErrUnknownFeature
		}
		options := scenario.Options(uint64(schedule.ChatID) ^ uint64(slot.Unix()))
		roundID, reserveErr := c.store.ReserveMovieRound(ctx, schedule.Feature, schedule.ChatID, slot.Unix(), slot.Add(defaultPollDuration).Unix(), options)
		if errors.Is(reserveErr, ErrActiveRound) || errors.Is(reserveErr, ErrDuplicate) || errors.Is(reserveErr, context.Canceled) {
			continue
		}
		if reserveErr != nil {
			return reserveErr
		}
		if roundID != 0 {
			c.log.Info("movieclub round planned", "round_id", roundID, "chat_id", schedule.ChatID, "feature", schedule.Feature, "manual", false)
		}
	}
	return nil
}

func (c *Coordinator) scheduleSlot(schedule Schedule, now time.Time) (time.Time, bool, error) {
	if !schedule.Enabled || !c.allowed[schedule.ChatID] || schedule.Zone == "" {
		return time.Time{}, false, nil
	}
	slot, err := WeeklySlot(now, schedule.Weekday, schedule.Clock, schedule.Zone)
	if err != nil {
		return time.Time{}, false, err
	}
	eligible := !now.Before(slot) && now.Before(slot.Add(catchupWindow)) && slot.Unix() > schedule.Effective
	return slot, eligible, nil
}

func (c *Coordinator) openPlanned(ctx context.Context, now time.Time) error {
	rounds, err := c.store.MovieRounds(ctx, StatePlanned)
	if err != nil {
		return err
	}
	for _, round := range rounds {
		if err = c.openRound(ctx, round, now); err != nil {
			return err
		}
	}
	return nil
}

func (c *Coordinator) openRound(ctx context.Context, round Round, now time.Time) error {
	if !c.allowed[round.ChatID] || round.NextAttempt > now.Unix() {
		return nil
	}
	claimed, err := c.store.ClaimMovieRound(ctx, round.ID, StatePlanned, StatePollCreating, now)
	if err != nil || !claimed {
		return err
	}
	options, err := c.store.MovieOptions(ctx, round.ID)
	if err != nil {
		return err
	}
	labels := make([]string, len(options))
	for i, option := range options {
		labels[i] = option.Label
	}
	closes := time.Unix(round.ClosesAt, 0)
	pollID, messageID, sendErr := c.telegram.OpenPoll(ctx, round.ChatID, round.Feature, labels)
	if sendErr != nil {
		return c.handleFailure(ctx, round, StatePollCreating, sendErr, now)
	}
	if err = c.store.OpenMovieRound(ctx, round.ID, pollID, messageID, now, closes); err != nil {
		return err
	}
	c.log.Info("movieclub poll opened", "round_id", round.ID, "chat_id", round.ChatID, "options", len(options))
	return nil
}

func (c *Coordinator) closeDue(ctx context.Context, now time.Time) error {
	rounds, err := c.store.MovieRounds(ctx, StateOpen)
	if err != nil {
		return err
	}
	for _, round := range rounds {
		if err = c.closeRound(ctx, round, now); err != nil {
			return err
		}
	}
	return nil
}

func (c *Coordinator) closeRound(ctx context.Context, round Round, now time.Time) error {
	if round.ClosesAt > now.Unix() || round.NextAttempt > now.Unix() {
		return nil
	}
	claimed, err := c.store.ClaimMovieRound(ctx, round.ID, StateOpen, StateClosing, now)
	if err != nil || !claimed {
		return err
	}
	votes, sendErr := c.telegram.ClosePoll(ctx, round.ChatID, round.PollMessageID)
	if sendErr != nil {
		return c.handleFailure(ctx, round, StateClosing, sendErr, now)
	}
	if err = c.store.SaveMoviePoll(ctx, round.ID, votes); err != nil {
		if errors.Is(err, ErrConflict) {
			return nil
		}
		return err
	}
	c.log.Info("movieclub poll tally saved", "round_id", round.ID, "chat_id", round.ChatID, "options", len(votes))
	return nil
}

func (c *Coordinator) prepareSelections(ctx context.Context, now time.Time) error {
	rounds, err := c.store.MovieRounds(ctx, StateSelecting)
	if err != nil {
		return err
	}
	for _, round := range rounds {
		if err = c.prepareSelection(ctx, round, now); err != nil {
			return err
		}
	}
	return nil
}

func (c *Coordinator) prepareSelection(ctx context.Context, round Round, now time.Time) error {
	if round.NextAttempt > now.Unix() {
		return nil
	}
	scenario := c.scenarios[round.Feature]
	if scenario == nil {
		return ErrUnknownFeature
	}
	options, err := c.store.MovieOptions(ctx, round.ID)
	if err != nil {
		return err
	}
	winners := scenario.Winners(options, uint64(round.ID))
	if len(winners) == 0 {
		return c.store.SaveMovieSelection(ctx, round.ID, "", nil)
	}
	movies, err := scenario.Recommendations(ctx, round, winners, now)
	if err != nil {
		return c.store.DeferMovieRound(ctx, round.ID, StateSelecting, StateSelecting, now.Add(time.Hour), catalogReason(err))
	}
	winnerNames := make([]string, len(winners))
	for i, winner := range winners {
		winnerNames[i] = winner.Label
	}
	if err = c.store.SaveMovieSelection(ctx, round.ID, strings.Join(winnerNames, " + "), movies); err != nil {
		return err
	}
	c.log.Info("movieclub selection prepared", "round_id", round.ID, "chat_id", round.ChatID, "winners", len(winners), "movies", len(movies))
	return nil
}

func (c *Coordinator) publishReady(ctx context.Context, now time.Time) error {
	rounds, err := c.store.MovieRounds(ctx, StateReady)
	if err != nil {
		return err
	}
	for _, round := range rounds {
		if err = c.publishRound(ctx, round, now); err != nil {
			return err
		}
	}
	return nil
}

func (c *Coordinator) publishRound(ctx context.Context, round Round, now time.Time) error {
	if round.NextAttempt > now.Unix() {
		return nil
	}
	claimed, err := c.store.ClaimMovieRound(ctx, round.ID, StateReady, StatePublishing, now)
	if err != nil || !claimed {
		return err
	}
	page1, err := c.store.MovieRecommendations(ctx, round.ID, 1)
	if err != nil {
		return err
	}
	if round.PublishStage == 0 {
		summary := Summary{Feature: round.Feature, Winner: round.Winner, Movies: page1, NoVotes: round.Winner == "" && len(page1) == 0}
		if _, sendErr := c.telegram.SendSummary(ctx, round.ChatID, summary, round.ID, round.Page2State == "ready"); sendErr != nil {
			return c.handleFailure(ctx, round, StatePublishing, sendErr, now)
		}
		if err = c.store.SetMoviePublishStage(ctx, round.ID, 1, StatePublishing); err != nil {
			return err
		}
	}
	if len(page1) > 0 {
		if _, sendErr := c.telegram.SendMovies(ctx, round.ChatID, page1); sendErr != nil {
			round.PublishStage = 1
			return c.handleFailure(ctx, round, StatePublishing, sendErr, now)
		}
	}
	if err = c.store.SetMoviePublishStage(ctx, round.ID, 2, StatePublished); err != nil {
		return err
	}
	c.log.Info("movieclub selection published", "round_id", round.ID, "chat_id", round.ChatID, "movies", len(page1))
	return nil
}

func (c *Coordinator) handleFailure(ctx context.Context, round Round, from State, err error, now time.Time) error {
	typed, ok := errors.AsType[*DeliveryError](err)
	target, code, next := StateUnknown, "telegram_unknown", time.Time{}
	if ok {
		switch typed.Kind {
		case DeliveryRetry:
			target, code, next = retryTarget(from), "telegram_rate_limit", now.Add(max(typed.After, time.Second))
		case DeliveryForbidden:
			target, code = StateFailed, "telegram_forbidden"
			if disableErr := c.store.DisableMovieSchedules(ctx, round.ChatID); disableErr != nil {
				return disableErr
			}
		case DeliveryPermanent:
			target, code = StateFailed, "telegram_rejected"
		}
	}
	c.log.Warn("movieclub operation failed", "round_id", round.ID, "chat_id", round.ChatID, "from", from, "target", target, "reason", code)
	return c.store.DeferMovieRound(context.WithoutCancel(ctx), round.ID, from, target, next, code)
}

func retryTarget(from State) State {
	switch from {
	case StatePollCreating:
		return StatePlanned
	case StateClosing:
		return StateOpen
	case StatePublishing:
		return StateReady
	default:
		return from
	}
}

func safeReason(err error) string {
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	return "internal"
}

func catalogReason(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "tmdb_timeout"
	}
	return "tmdb_unavailable"
}

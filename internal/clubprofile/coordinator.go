package clubprofile

import (
	"context"
	"errors"
	"time"
)

type Coordinator struct {
	service   *Service
	transport PollTransport
}

func NewCoordinator(service *Service, transport PollTransport) (*Coordinator, error) {
	if service == nil || transport == nil {
		return nil, ErrInvalid
	}
	return &Coordinator{service: service, transport: transport}, nil
}

func (c *Coordinator) Run(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		if err := c.Tick(ctx); err != nil && !errors.Is(err, context.Canceled) {
			c.service.log.ErrorContext(ctx, "profile cycle failed", "reason", "storage_or_validation")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (c *Coordinator) Tick(ctx context.Context) error {
	now := c.service.now()
	// Закрытие не зависит от успешности подготовки нового опроса.
	closeErr := c.closeDue(ctx, now)
	reviewErr := c.service.reviews.AdvanceProfileReviews(ctx, now)
	scheduleErr := c.reserveSchedules(ctx, now)
	openErr := c.openPlanned(ctx, now)
	return errors.Join(closeErr, reviewErr, scheduleErr, openErr)
}

func (c *Coordinator) reserveSchedules(ctx context.Context, now time.Time) error {
	schedules, err := c.service.schedules.ProfileSchedules(ctx, 0)
	if err != nil {
		return err
	}
	var errs []error
	for _, sc := range schedules {
		if !sc.Enabled || !c.service.allowed[sc.ChatID] {
			continue
		}
		if err = c.reserveSchedule(ctx, sc, now); err != nil && !errors.Is(err, ErrDuplicate) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (c *Coordinator) reserveSchedule(ctx context.Context, sc Schedule, now time.Time) error {
	slot, err := WeeklySlot(now, sc.Weekday, sc.Clock, sc.Zone)
	if err != nil {
		return err
	}
	if slot.After(now) || !slot.After(sc.Effective) || now.Sub(slot) > CatchupWindow {
		return nil
	}
	q, err := c.service.questions.InterestQuestion(ctx, sc.Topic, sc.ChatID, uint64(slot.Unix())^uint64(sc.ChatID), now)
	if err != nil {
		return err
	}
	q.Description = PollDescription(DefaultDuration)
	err = c.service.reviews.ReserveProfileSlot(ctx, sc, q, slot, now)
	if err == nil {
		c.service.log.InfoContext(ctx, "profile slot reserved", "chat_id", sc.ChatID, "schedule_id", sc.ID, "topic", sc.Topic)
	}
	return err
}

func (c *Coordinator) openPlanned(ctx context.Context, now time.Time) error {
	polls, err := c.service.polls.ProfilePolls(ctx, 0, Planned)
	if err != nil {
		return err
	}
	var errs []error
	for _, p := range polls {
		if !c.service.allowed[p.ChatID] || p.NextAttempt.After(now) {
			continue
		}
		if err = c.open(ctx, p, now); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (c *Coordinator) open(ctx context.Context, p Poll, now time.Time) error {
	if now.Sub(p.SlotAt) > CatchupWindow {
		return c.service.polls.DeferProfilePoll(ctx, p.ID, Planned, Cancelled, now, "slot_expired")
	}
	claimed, err := c.service.polls.ClaimProfilePoll(ctx, p.ID, Planned, Creating)
	if err != nil || !claimed {
		return err
	}
	pollID, messageID, err := c.transport.OpenProfilePoll(ctx, p)
	if err != nil {
		return c.failed(ctx, p, Creating, err, now)
	}
	persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	err = c.service.polls.OpenProfilePoll(persist, p.ID, pollID, messageID, c.service.now())
	if err != nil {
		return errors.Join(err, c.service.polls.DeferProfilePoll(persist, p.ID, Creating, Unknown, now, "receipt_not_saved"))
	}
	c.service.log.InfoContext(ctx, "profile poll opened", "poll_id", p.ID, "chat_id", p.ChatID, "options", len(p.Question.Options))
	return nil
}

func (c *Coordinator) closeDue(ctx context.Context, now time.Time) error {
	polls, err := c.service.polls.ProfilePolls(ctx, 0, Open)
	if err != nil {
		return err
	}
	var errs []error
	for _, p := range polls {
		if p.ClosesAt.After(now) || p.NextAttempt.After(now) || !c.service.allowed[p.ChatID] {
			continue
		}
		if err = c.close(ctx, p, now); err != nil && !errors.Is(err, ErrConflict) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (c *Coordinator) close(ctx context.Context, p Poll, now time.Time) error {
	claimed, err := c.service.polls.ClaimProfilePoll(ctx, p.ID, Open, Closing)
	if err != nil || !claimed {
		return err
	}
	result, err := c.transport.CloseProfilePoll(ctx, p)
	if err == nil {
		err = result.ValidateFinal(p.Question, p.TelegramID)
	}
	if err != nil {
		return c.failed(ctx, p, Closing, err, now)
	}
	persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_, err = c.service.polls.CompleteProfilePoll(persist, result)
	if err != nil && !errors.Is(err, ErrConflict) {
		return errors.Join(err, c.service.polls.DeferProfilePoll(persist, p.ID, Closing, Failed, now, "result_not_saved"))
	}
	return err
}

func (c *Coordinator) failed(ctx context.Context, p Poll, from PollState, cause error, now time.Time) error {
	to := Unknown
	reason := "transport_unknown"
	delay := time.Minute
	if from == Closing {
		to = Open
	}
	if e, ok := errors.AsType[*DeliveryError](cause); ok {
		reason = e.Reason
		switch e.Kind {
		case "retry":
			if from == Creating {
				to = Planned
			}
			if e.After > delay {
				delay = e.After
			}
		case "permanent", "forbidden":
			to = Failed
		}
	}
	if errors.Is(cause, ErrInvalid) {
		to = Failed
		reason = "result_incomplete"
	}
	// Повторное закрытие безопасно для доставки, но после окна восстановления финал неизвестен.
	if from == Closing && now.After(p.ClosesAt.Add(DefaultDuration)) {
		to = Failed
		reason = "result_missing"
	}
	c.service.log.WarnContext(ctx, "profile poll operation failed", "poll_id", p.ID, "chat_id", p.ChatID, "from", from, "to", to, "reason", reason)
	persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return c.service.polls.DeferProfilePoll(persist, p.ID, from, to, now.Add(delay), reason)
}

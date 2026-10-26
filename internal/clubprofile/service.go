package clubprofile

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

type Service struct {
	polls     PollRepository
	schedules ScheduleRepository
	recovery  RecoveryRepository
	reviews   ReviewRepository
	status    ProfileStatusRepository
	questions QuestionSource
	allowed   map[int64]bool
	log       *slog.Logger
	now       func() time.Time
}

type Repository interface {
	PollRepository
	ScheduleRepository
	RecoveryRepository
	ReviewRepository
	ProfileStatusRepository
}

func NewService(store Repository, questions QuestionSource, allowed map[int64]bool, log *slog.Logger, now func() time.Time) (*Service, error) {
	if store == nil || questions == nil || log == nil || now == nil {
		return nil, ErrInvalid
	}
	return &Service{polls: store, schedules: store, recovery: store, reviews: store, status: store, questions: questions, allowed: allowed, log: log, now: now}, nil
}

func (s *Service) Status(ctx context.Context, chat int64) (ProfileStatus, error) {
	if !s.allowed[chat] {
		return ProfileStatus{}, ErrInvalid
	}
	return s.status.ProfileStatus(ctx, chat)
}

func (s *Service) Start(ctx context.Context, topic Topic, operation, chat int64, duration time.Duration) (int64, error) {
	if !s.allowed[chat] || (topic != Genre && topic != Reference) || duration < 5*time.Minute || duration > DefaultDuration {
		return 0, ErrInvalid
	}
	now := s.now()
	q, err := s.questions.InterestQuestion(ctx, topic, chat, uint64(operation), now)
	if err != nil {
		return 0, err
	}
	q.Description = PollDescription(duration)
	id, err := s.polls.PlanProfilePoll(ctx, &operation, Poll{ChatID: chat, Question: q, SlotAt: now, Duration: duration})
	if err == nil {
		s.log.InfoContext(ctx, "profile poll planned", "chat_id", chat, "poll_id", id, "topic", topic, "manual", true)
	}
	return id, err
}

func PollDescription(duration time.Duration) string {
	return fmt.Sprintf("🗳 На ответ — %s\nПрофиль клуба: /movie_profile", DurationText(duration))
}

func DurationText(d time.Duration) string {
	if d%time.Hour == 0 {
		return fmt.Sprintf("%d ч.", int(d/time.Hour))
	}
	if d%time.Minute == 0 {
		return fmt.Sprintf("%d мин.", int(d/time.Minute))
	}
	return fmt.Sprintf("%d мин. %d сек.", int(d/time.Minute), int((d%time.Minute)/time.Second))
}

func (s *Service) SetSchedule(ctx context.Context, operation int64, p Schedule) error {
	if !s.allowed[p.ChatID] {
		return ErrInvalid
	}
	p.Effective = s.now()
	return s.schedules.SetProfileSchedule(ctx, operation, p)
}

func (s *Service) PauseSchedules(ctx context.Context, operation, chat int64, topic Topic) error {
	if !s.allowed[chat] {
		return ErrInvalid
	}
	return s.schedules.PauseProfileSchedules(ctx, operation, chat, topic, s.now())
}

func (s *Service) Settings(ctx context.Context, chat int64) ([]Schedule, []Poll, error) {
	if !s.allowed[chat] {
		return nil, nil, ErrInvalid
	}
	schedules, err := s.schedules.ProfileSchedules(ctx, chat)
	if err != nil {
		return nil, nil, err
	}
	polls, err := s.polls.ProfilePolls(ctx, chat, "")
	return schedules, polls, err
}

func (s *Service) PollClosed(ctx context.Context, result PollResult) error {
	applied, err := s.polls.CompleteProfilePoll(ctx, result)
	if err == nil && applied {
		s.log.InfoContext(ctx, "profile poll finalized", "source", result.Source, "options", len(result.Counts))
	}
	return err
}

func (s *Service) Resolve(ctx context.Context, operation, chat, id int64, action string) error {
	if !s.allowed[chat] {
		return ErrInvalid
	}
	return s.recovery.ResolveProfilePoll(ctx, operation, chat, id, action, s.now())
}

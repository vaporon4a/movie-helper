package clubreminder

import (
	"context"
	"log/slog"
	"time"
)

// CommandRepository contains only data operations needed by reminder commands.
type CommandRepository interface {
	SetClubSchedule(context.Context, int64, int64, int, int, string, time.Time) error
	PauseClub(context.Context, int64, int64, time.Time) error
	ClubSettings(context.Context, int64, time.Time) (Settings, error)
	ClubTexts(context.Context, int64, time.Time) ([]Text, error)
	ResolveClub(context.Context, int64, int64, int64, string, time.Time) error
}

type Service struct {
	store CommandRepository
	now   func() time.Time
	log   *slog.Logger
}

func NewService(store CommandRepository, now func() time.Time, log *slog.Logger) *Service {
	if now == nil {
		now = time.Now
	}
	if log == nil {
		log = slog.Default()
	}
	return &Service{store: store, now: now, log: log}
}
func (s *Service) SetSchedule(ctx context.Context, op, chat int64, weekday, ordinal int, clock string) error {
	if err := Validate(weekday, ordinal, clock); err != nil {
		return err
	}
	if err := s.store.SetClubSchedule(ctx, op, chat, weekday, ordinal, clock, s.now()); err != nil {
		return err
	}
	s.log.Info("club reminder schedule changed", "chat_id", chat, "weekday", weekday, "ordinal", ordinal, "clock", clock)
	return nil
}
func (s *Service) Pause(ctx context.Context, op, chat int64) error {
	if err := s.store.PauseClub(ctx, op, chat, s.now()); err != nil {
		return err
	}
	s.log.Info("club reminders paused", "chat_id", chat)
	return nil
}
func (s *Service) Settings(ctx context.Context, chat int64) (Settings, error) {
	return s.store.ClubSettings(ctx, chat, s.now())
}
func (s *Service) Preview(ctx context.Context, chat int64) (Text, error) {
	texts, err := s.store.ClubTexts(ctx, chat, s.now())
	if err != nil {
		return Text{}, err
	}
	for _, text := range texts {
		if text.State == Ready {
			return text, nil
		}
	}
	return Fallback(Explore, RandomTone()), nil
}
func (s *Service) Resolve(ctx context.Context, op, chat, id int64, action string) error {
	if err := s.store.ResolveClub(ctx, op, chat, id, action, s.now()); err != nil {
		return err
	}
	s.log.Info("club reminder delivery resolved", "chat_id", chat, "delivery_id", id, "action", action)
	return nil
}

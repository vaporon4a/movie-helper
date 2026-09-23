package daily

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

type Repository interface {
	EnsureChat(context.Context, int64) error
	Suspend(context.Context, int64) error
	Schedules(context.Context, int64) ([]Schedule, error)
	Issues(context.Context, int64) ([]Delivery, error)
	SetModeration(context.Context, int64, int64, bool, time.Time) error
	SetZone(context.Context, int64, int64, string, time.Time) error
	SetSchedule(context.Context, int64, int64, string, string, bool, time.Time) error
	Resume(context.Context, int64, int64) error
	Add(context.Context, int64, Item, time.Time) (int64, error)
	Queue(context.Context, int64, int64) ([]Item, error)
	Item(context.Context, int64, int64) (Item, error)
	Moderate(context.Context, int64, int64, int64, int64, bool, time.Time) error
	Resolve(context.Context, int64, int64, int64, bool) error
}

type Provider interface {
	Candidates(context.Context, string, int64) ([]Item, error)
}

type Service struct {
	repository Repository
	provider   Provider
	log        *slog.Logger
}

func NewService(repository Repository, provider Provider, log *slog.Logger) (*Service, error) {
	if repository == nil {
		return nil, errors.New("daily service repository is required")
	}
	if log == nil {
		log = slog.Default()
	}
	return &Service{repository: repository, provider: provider, log: log}, nil
}

func (s *Service) EnsureChat(ctx context.Context, chatID int64) error {
	return s.repository.EnsureChat(ctx, chatID)
}
func (s *Service) Suspend(ctx context.Context, chatID int64) error {
	return s.repository.Suspend(ctx, chatID)
}
func (s *Service) Schedules(ctx context.Context, chatID int64) ([]Schedule, error) {
	return s.repository.Schedules(ctx, chatID)
}
func (s *Service) Issues(ctx context.Context, chatID int64) ([]Delivery, error) {
	return s.repository.Issues(ctx, chatID)
}
func (s *Service) SetModeration(ctx context.Context, operationID, chatID int64, enabled bool, now time.Time) error {
	err := s.repository.SetModeration(ctx, operationID, chatID, enabled, now)
	if err == nil {
		s.log.Info("daily moderation changed", "chat_id", chatID, "enabled", enabled)
	}
	return err
}
func (s *Service) SetZone(ctx context.Context, operationID, chatID int64, zone string, now time.Time) error {
	return s.repository.SetZone(ctx, operationID, chatID, zone, now)
}
func (s *Service) SetSchedule(ctx context.Context, operationID, chatID int64, kind, clock string, enabled bool, now time.Time) error {
	err := s.repository.SetSchedule(ctx, operationID, chatID, kind, clock, enabled, now)
	if err == nil {
		s.log.Info("daily schedule changed", "chat_id", chatID, "kind", kind, "enabled", enabled)
	}
	return err
}
func (s *Service) Resume(ctx context.Context, operationID, chatID int64) error {
	return s.repository.Resume(ctx, operationID, chatID)
}
func (s *Service) Add(ctx context.Context, operationID int64, item Item, now time.Time) (int64, error) {
	return s.repository.Add(ctx, operationID, item, now)
}
func (s *Service) Queue(ctx context.Context, chatID, after int64) ([]Item, error) {
	return s.repository.Queue(ctx, chatID, after)
}
func (s *Service) Item(ctx context.Context, chatID, itemID int64) (Item, error) {
	return s.repository.Item(ctx, chatID, itemID)
}
func (s *Service) Moderate(ctx context.Context, operationID, chatID, itemID, userID int64, approve bool, now time.Time) error {
	return s.repository.Moderate(ctx, operationID, chatID, itemID, userID, approve, now)
}
func (s *Service) Resolve(ctx context.Context, operationID, chatID, deliveryID int64, sent bool) error {
	return s.repository.Resolve(ctx, operationID, chatID, deliveryID, sent)
}
func (s *Service) Candidates(ctx context.Context, kind string, chatID int64) ([]Item, error) {
	if s.provider == nil {
		return nil, ErrProviderDisabled
	}
	items, err := s.provider.Candidates(ctx, kind, chatID)
	if err == nil {
		s.log.Info("daily preview prepared", "chat_id", chatID, "kind", kind, "candidates", len(items))
	}
	return items, err
}

var ErrProviderDisabled = errors.New("daily content provider is disabled")

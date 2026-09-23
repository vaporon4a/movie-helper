package movieclub

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

const defaultPollDuration = 24 * time.Hour

type Service struct {
	store    ServiceRepository
	telegram Transport
	scenario Scenario
	log      *slog.Logger
	now      func() time.Time
}

func NewService(store ServiceRepository, telegram Transport, scenario Scenario, log *slog.Logger, now func() time.Time) (*Service, error) {
	if store == nil || telegram == nil || scenario == nil {
		return nil, errors.New("movieclub service dependencies are required")
	}
	if log == nil {
		log = slog.Default()
	}
	if now == nil {
		now = time.Now
	}
	return &Service{store: store, telegram: telegram, scenario: scenario, log: log, now: now}, nil
}

func (s *Service) Start(ctx context.Context, operationID, chatID int64, duration time.Duration) (int64, error) {
	if duration < 5*time.Minute || duration > defaultPollDuration {
		return 0, errors.New("duration must be 5m..24h")
	}
	now := s.now()
	options := s.scenario.Options(uint64(chatID) ^ uint64(now.Unix()/60))
	roundID, err := s.store.StartMovieRound(ctx, s.scenario.Feature(), operationID, chatID, now, duration, options)
	if err == nil {
		s.log.Info("movieclub round planned", "round_id", roundID, "chat_id", chatID, "feature", s.scenario.Feature(), "manual", true)
	}
	return roundID, err
}

func (s *Service) SetSchedule(ctx context.Context, operationID, chatID int64, weekday int, clock string, enabled bool) error {
	return s.store.SetMovieSchedule(ctx, operationID, chatID, weekday, clock, enabled, s.now())
}

func (s *Service) PauseSchedules(ctx context.Context, operationID, chatID int64) error {
	return s.store.PauseMovieSchedules(ctx, operationID, chatID, s.now())
}

func (s *Service) Settings(ctx context.Context, chatID int64) (SettingsView, error) {
	schedules, err := s.store.MovieSchedules(ctx, chatID)
	if err != nil {
		return SettingsView{}, err
	}
	rounds, err := s.store.LatestMovieRounds(ctx, chatID)
	if err != nil {
		return SettingsView{}, err
	}
	return SettingsView{Schedules: schedules, Rounds: rounds}, nil
}

func (s *Service) PollClosed(ctx context.Context, pollID string, votes []int) error {
	if pollID == "" {
		return ErrConflict
	}
	err := s.store.SaveMoviePollByID(ctx, pollID, votes)
	if err == nil {
		s.log.Info("movieclub poll closed", "poll_id_present", true, "options", len(votes))
	}
	return err
}

func (s *Service) More(ctx context.Context, chatID, roundID int64) error {
	claimed, err := s.store.ClaimMoviePage2(ctx, chatID, roundID)
	if err != nil || !claimed {
		if err == nil {
			err = ErrConflict
		}
		return err
	}
	movies, err := s.store.MovieRecommendations(ctx, roundID, 2)
	if err != nil {
		_ = s.store.FinishMoviePage2(context.WithoutCancel(ctx), chatID, roundID, "ready")
		return err
	}
	_, err = s.telegram.SendMovies(ctx, chatID, movies)
	state := "sent"
	if err != nil {
		state = pageFailure(err)
	}
	persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if saveErr := s.store.FinishMoviePage2(persist, chatID, roundID, state); saveErr != nil {
		return saveErr
	}
	return err
}

func (s *Service) Resolve(ctx context.Context, operationID, chatID, roundID int64, action ResolveAction) error {
	return s.store.ResolveMovieRound(ctx, operationID, chatID, roundID, action)
}

func pageFailure(err error) string {
	typed, ok := errors.AsType[*DeliveryError](err)
	if ok && typed.Kind == DeliveryRetry {
		return "ready"
	}
	return "unknown"
}

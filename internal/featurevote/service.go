package featurevote

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"
)

type Service struct {
	store ServiceRepository
	log   *slog.Logger
	now   func() time.Time
	token func() (string, error)
}

func NewService(store ServiceRepository, log *slog.Logger, now func() time.Time, token func() (string, error)) (*Service, error) {
	if store == nil || token == nil {
		return nil, errors.New("feature vote service dependencies are required")
	}
	if log == nil {
		log = slog.Default()
	}
	if now == nil {
		now = time.Now
	}
	return &Service{store: store, log: log, now: now, token: token}, nil
}

func (s *Service) Add(ctx context.Context, operationID, chatID, authorID int64, text string) (Idea, error) {
	text = strings.TrimSpace(text)
	if n := utf8.RuneCountInString(text); n < MinIdeaRunes || n > MaxIdeaRunes {
		return Idea{}, errors.New("feature idea must be 20..1500 characters")
	}
	normalized := strings.ToLower(strings.Join(strings.Fields(text), " "))
	sum := sha256.Sum256([]byte(normalized))
	idea, err := s.store.AddFeature(ctx, operationID, chatID, authorID, text, hex.EncodeToString(sum[:]), s.now())
	if err == nil {
		s.log.Info("feature idea added", "chat_id", chatID, "idea_id", idea.ID)
	}
	return idea, err
}

func (s *Service) Mine(ctx context.Context, chatID, authorID int64) ([]Idea, error) {
	return s.store.AuthorFeatures(ctx, chatID, authorID)
}

func (s *Service) Backlog(ctx context.Context, chatID int64) ([]Idea, error) {
	return s.store.FeatureBacklog(ctx, chatID)
}

func (s *Service) ChangeState(ctx context.Context, operationID, chatID, ideaID, actorID int64, state IdeaState, admin bool) error {
	if state != IdeaActive && state != IdeaImplemented && state != IdeaRemoved {
		return ErrConflict
	}
	return s.store.ChangeFeatureState(ctx, operationID, chatID, ideaID, actorID, state, admin, s.now())
}

func (s *Service) SetSchedule(ctx context.Context, operationID, chatID int64, weekday int, clock string) error {
	return s.store.SetFeatureSchedule(ctx, operationID, chatID, weekday, clock, true, s.now())
}

func (s *Service) Pause(ctx context.Context, operationID, chatID int64) error {
	return s.store.PauseFeatureSchedule(ctx, operationID, chatID, s.now())
}

func (s *Service) Settings(ctx context.Context, chatID int64) (SettingsView, error) {
	return s.store.FeatureSettings(ctx, chatID)
}

func (s *Service) Start(ctx context.Context, operationID, chatID int64, duration time.Duration) (int64, error) {
	if duration < MinRoundDuration || duration > DefaultRoundDuration {
		return 0, errors.New("feature vote duration must be 5m..24h")
	}
	token, err := s.token()
	if err != nil {
		return 0, err
	}
	return s.store.StartFeatureRound(ctx, operationID, chatID, s.now(), duration, token)
}

func (s *Service) View(ctx context.Context, token string, userID int64) (View, error) {
	return s.store.FeatureView(ctx, token, userID)
}

func (s *Service) Vote(ctx context.Context, token string, userID, ideaID int64) error {
	return s.store.VoteFeature(ctx, token, userID, ideaID, s.now())
}

func (s *Service) PollClosed(ctx context.Context, pollID string, votes []int) error {
	if pollID == "" {
		return ErrConflict
	}
	return s.store.SaveFeaturePollByID(ctx, pollID, votes)
}

func (s *Service) Resolve(ctx context.Context, operationID, chatID, roundID int64, action ResolveAction) error {
	if action != ResolveSent && action != ResolveRetry && action != ResolveCancel {
		return ErrConflict
	}
	return s.store.ResolveFeatureRound(ctx, operationID, chatID, roundID, action, s.now())
}

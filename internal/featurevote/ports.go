package featurevote

import (
	"context"
	"time"
)

type ServiceRepository interface {
	AddFeature(context.Context, int64, int64, int64, string, string, time.Time) (Idea, error)
	AuthorFeatures(context.Context, int64, int64) ([]Idea, error)
	FeatureBacklog(context.Context, int64) ([]Idea, error)
	ChangeFeatureState(context.Context, int64, int64, int64, int64, IdeaState, bool, time.Time) error
	SetFeatureSchedule(context.Context, int64, int64, int, string, bool, time.Time) error
	PauseFeatureSchedule(context.Context, int64, int64, time.Time) error
	FeatureSettings(context.Context, int64) (SettingsView, error)
	StartFeatureRound(context.Context, int64, int64, time.Time, time.Duration, string) (int64, error)
	FeatureView(context.Context, string, int64) (View, error)
	VoteFeature(context.Context, string, int64, int64, time.Time) error
	ResolveFeatureRound(context.Context, int64, int64, int64, ResolveAction, time.Time) error
}

type CoordinatorRepository interface {
	FeatureSchedules(context.Context, int64) ([]Schedule, error)
	ReserveFeatureRound(context.Context, int64, int64, int64, string) (int64, error)
	FeatureRounds(context.Context, RoundState) ([]Round, error)
	FeatureRound(context.Context, int64) (Round, error)
	FeatureRoundOptions(context.Context, int64) ([]Option, error)
	ActiveFeatures(context.Context, int64) ([]Idea, error)
	SaveFeatureTitle(context.Context, int64, string) error
	SaveFeatureRoundOptions(context.Context, int64, []Option) error
	ClaimFeatureRound(context.Context, int64, RoundState, RoundState, time.Time) (bool, error)
	OpenFeatureRound(context.Context, int64, int, time.Time) error
	CancelEmptyFeatureRound(context.Context, int64) error
	FinalizeFeatureRound(context.Context, int64, string, time.Time) (Round, error)
	PublishFeatureRound(context.Context, int64) error
	DeferFeatureRound(context.Context, int64, RoundState, RoundState, time.Time, string) error
	DisableFeatureSchedule(context.Context, int64) error
}

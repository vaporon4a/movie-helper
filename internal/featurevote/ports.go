package featurevote

import (
	"context"
	"time"
)

// ServiceRepository is used only to assemble narrower consumer ports.
type ServiceRepository interface {
	IdeaRepository
	ServiceScheduleRepository
	ServiceRoundRepository
}

type IdeaRepository interface {
	AddFeature(context.Context, int64, int64, int64, string, string, time.Time) (Idea, error)
	AuthorFeatures(context.Context, int64, int64) ([]Idea, error)
	FeatureBacklog(context.Context, int64) ([]Idea, error)
	ChangeFeatureState(context.Context, int64, int64, int64, int64, IdeaState, bool, time.Time) error
}

type ServiceScheduleRepository interface {
	SetFeatureSchedule(context.Context, int64, int64, int, string, bool, time.Time) error
	PauseFeatureSchedule(context.Context, int64, int64, time.Time) error
	FeatureSettings(context.Context, int64) (SettingsView, error)
}

type ServiceRoundRepository interface {
	StartFeatureRound(context.Context, int64, int64, time.Time, time.Duration, string) (int64, error)
	FeatureView(context.Context, string, int64) (View, error)
	VoteFeature(context.Context, string, int64, int64, time.Time) error
	SaveFeaturePollByID(context.Context, string, []int) error
	ResolveFeatureRound(context.Context, int64, int64, int64, ResolveAction, time.Time) error
}

// CoordinatorRepository is used only to assemble narrower consumer ports.
type CoordinatorRepository interface {
	CoordinatorScheduleRepository
	CoordinatorRoundRepository
	OptionRepository
	FinalizationRepository
}

type CoordinatorScheduleRepository interface {
	FeatureSchedules(context.Context, int64) ([]Schedule, error)
	ReserveFeatureRound(context.Context, int64, int64, int64, string) (int64, error)
	DisableFeatureSchedule(context.Context, int64) error
}

type CoordinatorRoundRepository interface {
	FeatureRounds(context.Context, RoundState) ([]Round, error)
	FeatureRound(context.Context, int64) (Round, error)
	FeatureRoundOptions(context.Context, int64) ([]Option, error)
	ClaimFeatureRound(context.Context, int64, RoundState, RoundState, time.Time) (bool, error)
	OpenFeatureRound(context.Context, int64, OpenResult, time.Time) error
	SaveFeaturePoll(context.Context, int64, []int) error
	DeferFeatureRound(context.Context, int64, RoundState, RoundState, time.Time, string) error
}

type OptionRepository interface {
	ActiveFeatures(context.Context, int64) ([]Idea, error)
	SaveFeatureTitle(context.Context, int64, string) error
	SaveFeatureRoundOptions(context.Context, int64, []Option) error
}

type FinalizationRepository interface {
	CancelEmptyFeatureRound(context.Context, int64) error
	FinalizeFeatureRound(context.Context, int64, string, time.Time) (Round, error)
	PublishFeatureRound(context.Context, int64) error
}

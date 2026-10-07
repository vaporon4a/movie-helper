package movieclub

import (
	"context"
	"time"
)

// ServiceRepository is used only to assemble narrower consumer ports.
type ServiceRepository interface {
	ServiceScheduleRepository
	ServiceRoundRepository
	ServicePageRepository
	PreferenceRepository
}

type ServiceScheduleRepository interface {
	SetMovieSchedule(context.Context, Feature, int64, int64, int, string, bool, time.Time) error
	PauseMovieSchedules(context.Context, Feature, int64, int64, time.Time) error
	MovieSchedules(context.Context, int64) ([]Schedule, error)
}

type ServiceRoundRepository interface {
	StartMovieRound(context.Context, Feature, int64, int64, time.Time, time.Duration, []Option) (int64, error)
	LatestMovieRounds(context.Context, int64) ([]Round, error)
	MovieRound(context.Context, int64, int64) (Round, error)
	SaveMoviePollByID(context.Context, string, []int) error
	ResolveMovieRound(context.Context, int64, int64, int64, ResolveAction, time.Time) error
}

type ServicePageRepository interface {
	MovieRecommendations(context.Context, int64, int) ([]Recommendation, error)
	ClaimMoviePage2(context.Context, int64, int64) (bool, error)
	FinishMoviePage2(context.Context, int64, int64, string) error
}

type PreferenceRepository interface {
	MoviePreferenceSettings(context.Context, int64, time.Time) (PreferenceSettings, error)
	SetMoviePreferenceMode(context.Context, int64, int64, PersonalizationMode, time.Time) error
	ResetMovieTaste(context.Context, int64, int64, time.Time) error
	MovieTasteHistory(context.Context, int64, int64, time.Time) ([]TasteRound, error)
}

// CoordinatorRepository is used only to assemble narrower consumer ports.
type CoordinatorRepository interface {
	CoordinatorScheduleRepository
	CoordinatorRoundRepository
	SelectionRepository
}

type CoordinatorScheduleRepository interface {
	MovieSchedules(context.Context, int64) ([]Schedule, error)
	ReserveMovieRound(context.Context, Feature, int64, int64, int64, []Option) (int64, error)
	DisableMovieSchedules(context.Context, int64) error
}

type CoordinatorRoundRepository interface {
	MovieRounds(context.Context, State) ([]Round, error)
	MovieOptions(context.Context, int64) ([]Option, error)
	SaveMovieOptions(context.Context, int64, []Option) error
	ClaimMovieRound(context.Context, int64, State, State, time.Time) (bool, error)
	OpenMovieRound(context.Context, int64, string, int, time.Time, time.Time) error
	DeferMovieRound(context.Context, int64, State, State, time.Time, string) error
	SaveMoviePoll(context.Context, int64, []int) error
}

type SelectionRepository interface {
	SaveMovieSelection(context.Context, int64, string, Selection) error
	MovieRecommendations(context.Context, int64, int) ([]Recommendation, error)
	SetMoviePublishStage(context.Context, int64, int, State) error
}

type RecommendationHistory interface {
	RecentMovieIDs(context.Context, int64, time.Time) (map[int64]bool, error)
}

type ReferenceHistory interface {
	RecommendationHistory
	RecentReferenceSeedIDs(context.Context, int64, time.Time) (map[int64]bool, error)
}

type TasteHistory interface {
	MoviePreferenceSettings(context.Context, int64, time.Time) (PreferenceSettings, error)
	MovieTasteHistory(context.Context, int64, int64, time.Time) ([]TasteRound, error)
	MovieExposureCounts(context.Context, int64, time.Time) (map[int64]int, error)
}

type TasteMetadataStore interface {
	SaveMovieOptionMetadata(context.Context, int64, int, MovieMetadata) error
}

type Transport interface {
	OpenPoll(context.Context, int64, Feature, []string) (string, int, error)
	ClosePoll(context.Context, int64, int) ([]int, error)
	SendSummary(context.Context, int64, Summary, int64, bool) (int, error)
	SendMovies(context.Context, int64, []Recommendation) ([]int, error)
}

type Scenario interface {
	Feature() Feature
	Options(context.Context, int64, uint64, time.Time) ([]Option, error)
	Winners([]Option, uint64) []Option
	Recommendations(context.Context, Round, []Option, time.Time) (Selection, error)
}

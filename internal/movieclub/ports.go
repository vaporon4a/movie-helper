package movieclub

import (
	"context"
	"time"
)

type ServiceRepository interface {
	SetMovieSchedule(context.Context, int64, int64, int, string, bool, time.Time) error
	PauseMovieSchedules(context.Context, int64, int64, time.Time) error
	MovieSchedules(context.Context, int64) ([]Schedule, error)
	StartMovieRound(context.Context, Feature, int64, int64, time.Time, time.Duration, []Option) (int64, error)
	LatestMovieRounds(context.Context, int64) ([]Round, error)
	SaveMoviePollByID(context.Context, string, []int) error
	MovieRecommendations(context.Context, int64, int) ([]Recommendation, error)
	ClaimMoviePage2(context.Context, int64, int64) (bool, error)
	FinishMoviePage2(context.Context, int64, int64, string) error
	ResolveMovieRound(context.Context, int64, int64, int64, ResolveAction) error
}

type CoordinatorRepository interface {
	MovieSchedules(context.Context, int64) ([]Schedule, error)
	ReserveMovieRound(context.Context, Feature, int64, int64, int64, []Option) (int64, error)
	MovieRounds(context.Context, State) ([]Round, error)
	MovieOptions(context.Context, int64) ([]Option, error)
	ClaimMovieRound(context.Context, int64, State, State, time.Time) (bool, error)
	OpenMovieRound(context.Context, int64, string, int, time.Time, time.Time) error
	DeferMovieRound(context.Context, int64, State, State, time.Time, string) error
	SaveMoviePoll(context.Context, int64, []int) error
	SaveMovieSelection(context.Context, int64, string, []Recommendation) error
	MovieRecommendations(context.Context, int64, int) ([]Recommendation, error)
	SetMoviePublishStage(context.Context, int64, int, State) error
	DisableMovieSchedules(context.Context, int64) error
}

type RecommendationHistory interface {
	RecentMovieIDs(context.Context, int64, time.Time) (map[int64]bool, error)
}

type Transport interface {
	OpenPoll(context.Context, int64, Feature, []string) (string, int, error)
	ClosePoll(context.Context, int64, int) ([]int, error)
	SendSummary(context.Context, int64, Summary, int64, bool) (int, error)
	SendMovies(context.Context, int64, []Recommendation) ([]int, error)
}

type Scenario interface {
	Feature() Feature
	Options(uint64) []Option
	Winners([]Option, uint64) []Option
	Recommendations(context.Context, Round, []Option, time.Time) ([]Recommendation, error)
}

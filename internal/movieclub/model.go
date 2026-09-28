// Package movieclub implements scheduled movie polls independently from daily content.
package movieclub

import (
	"context"
	"errors"
	"time"
)

type Feature string
type State string
type DeliveryKind string
type ResolveAction string
type OptionKind string
type DiscoverSort string
type PersonalizationMode string
type SelectionRole string

const (
	Genre       Feature    = "genre"
	Reference   Feature    = "reference"
	OptionGenre OptionKind = "genre"
	OptionMovie OptionKind = "movie"

	DiscoverByRating      DiscoverSort        = "vote_average.desc"
	PersonalizationOff    PersonalizationMode = "off"
	PersonalizationShadow PersonalizationMode = "shadow"
	PersonalizationOn     PersonalizationMode = "on"
	SelectionLegacy       SelectionRole       = "legacy"
	SelectionExploit      SelectionRole       = "exploit"
	SelectionExplore      SelectionRole       = "explore"
	SelectionWildcard     SelectionRole       = "wildcard"
	RankingPolicyV1                           = "taste-v1"
	LegacyPolicyVersion                       = "legacy"

	DeliveryRetry     DeliveryKind = "retry"
	DeliveryForbidden DeliveryKind = "forbidden"
	DeliveryPermanent DeliveryKind = "permanent"
	DeliveryUnknown   DeliveryKind = "unknown"

	ResolveSent   ResolveAction = "sent"
	ResolveRetry  ResolveAction = "retry"
	ResolveCancel ResolveAction = "cancel"

	StatePlanned      State = "planned"
	StatePollCreating State = "poll_creating"
	StateOpen         State = "open"
	StateClosing      State = "closing"
	StateSelecting    State = "selecting"
	StateReady        State = "ready"
	StatePublishing   State = "publishing"
	StatePublished    State = "published"
	StateCancelled    State = "cancelled"
	StateFailed       State = "failed"
	StateUnknown      State = "unknown"
)

func (f Feature) Valid() bool { return f == Genre || f == Reference }

var (
	ErrActiveRound = errors.New("movie poll already active")
	ErrConflict    = errors.New("movie poll state changed")
	ErrDuplicate   = errors.New("movie poll already planned")
	ErrDisabled    = errors.New("movie polls are not configured")
)

type Schedule struct {
	ID, ChatID, Effective int64
	Feature               Feature
	Clock, Zone           string
	Weekday               int
	Enabled               bool
}

type Option struct {
	RoundID, ProviderID int64
	Position, Votes     int
	Kind                OptionKind
	Label               string
	Metadata            MovieMetadata
	SelectionRole       SelectionRole
	SelectionScore      float64
	PolicyVersion       string
}

type Movie struct {
	ID                 int64
	Title, Overview    string
	PosterPath         string
	Year, VoteCount    int
	Rating, Popularity float64
	Genres             []int64
}

type MovieMetadata struct {
	GenreIDs    []int64 `json:"genre_ids,omitempty"`
	ReleaseYear int     `json:"release_year,omitempty"`
}

type Credit struct {
	PersonID int64
	Name     string
	Job      string
}

type MovieDetails struct {
	Movie
	Crew []Credit
}

type PersonMovie struct {
	Movie
	Job string
}

type Recommendation struct {
	Movie
	RoundID        int64
	Page, Position int
	Relation       string
	RankingScore   float64
	Ranking        RankingBreakdown
	PolicyVersion  string
}

type RankingBreakdown struct {
	Quality         float64 `json:"quality"`
	Affinity        float64 `json:"affinity"`
	Novelty         float64 `json:"novelty"`
	Exploration     float64 `json:"exploration"`
	SourceRelevance float64 `json:"source_relevance"`
}

type RankingCandidate struct {
	RoundID, TMDBID                  int64
	SourceBucket, SelectedMode       string
	Movie                            Movie
	LegacyPosition, AdaptivePosition int
	RankingScore                     float64
	Ranking                          RankingBreakdown
	PolicyVersion                    string
}

type Selection struct {
	Hero          Movie
	Movies        []Recommendation
	Candidates    []RankingCandidate
	Mode          PersonalizationMode
	PolicyVersion string
}

type PreferenceSettings struct {
	ChatID        int64
	Mode          PersonalizationMode
	EffectiveFrom int64
	PolicyVersion string
	UpdatedAt     int64
}

func (m PersonalizationMode) Valid() bool {
	return m == PersonalizationOff || m == PersonalizationShadow || m == PersonalizationOn
}

type TasteRound struct {
	ID, ClosedAt int64
	Feature      Feature
	Options      []Option
}

type ChatTasteProfile struct {
	GenreAffinity   map[int64]float64
	DecadeAffinity  map[int]float64
	GenreExposure   map[int64]int
	DecadeExposure  map[int]int
	EffectiveVotes  float64
	CompletedRounds int
	SkippedRecords  int
}

type Round struct {
	ID, ChatID, SlotAt, OpenedAt, ClosesAt, NextAttempt int64
	PollMessageID, PublishStage                         int
	Feature                                             Feature
	State                                               State
	PollID, Winner, ResultText                          string
	ErrorCode, Page2State                               string
	Options                                             []Option
	Hero                                                Movie
}

type SettingsView struct {
	Schedules  []Schedule
	Rounds     []Round
	Preference PreferenceSettings
}

type Summary struct {
	Feature Feature
	Winner  string
	Movies  []Recommendation
	Hero    Movie
	Total   int
	NoVotes bool
}

type DiscoverQuery struct {
	GenreID        int64
	Page, MinVotes int
	FromDate       time.Time
	ToDate         time.Time
	Sort           DiscoverSort
}

type DeliveryError struct {
	Kind   DeliveryKind
	After  time.Duration
	Reason string
}

func (e *DeliveryError) Error() string { return "movie delivery: " + string(e.Kind) }

type Catalog interface {
	Discover(context.Context, DiscoverQuery) ([]Movie, error)
	PosterURL(path string) string
}

type ReferenceCatalog interface {
	Details(context.Context, int64) (MovieDetails, error)
	Recommendations(context.Context, int64) ([]Movie, error)
	Similar(context.Context, int64) ([]Movie, error)
	PersonMovies(context.Context, int64) ([]PersonMovie, error)
}

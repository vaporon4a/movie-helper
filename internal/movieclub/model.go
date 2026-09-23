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

const (
	Genre       Feature    = "genre"
	Reference   Feature    = "reference"
	OptionGenre OptionKind = "genre"
	OptionMovie OptionKind = "movie"

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
}

type Movie struct {
	ID                 int64
	Title, Overview    string
	PosterPath         string
	Year, VoteCount    int
	Rating, Popularity float64
}

type Recommendation struct {
	Movie
	RoundID        int64
	Page, Position int
	Relation       string
}

type Round struct {
	ID, ChatID, SlotAt, OpenedAt, ClosesAt, NextAttempt int64
	PollMessageID, PublishStage                         int
	Feature                                             Feature
	State                                               State
	PollID, Winner, ResultText                          string
	ErrorCode, Page2State                               string
	Options                                             []Option
}

type SettingsView struct {
	Schedules []Schedule
	Rounds    []Round
}

type Summary struct {
	Feature Feature
	Winner  string
	Movies  []Recommendation
	NoVotes bool
}

type DeliveryError struct {
	Kind  DeliveryKind
	After time.Duration
}

func (e *DeliveryError) Error() string { return "movie delivery: " + string(e.Kind) }

type Catalog interface {
	Discover(ctx context.Context, genreID int64, page, minVotes int) ([]Movie, error)
	PosterURL(path string) string
}

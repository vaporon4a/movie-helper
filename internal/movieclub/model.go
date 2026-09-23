// Package movieclub implements scheduled movie polls independently from daily content.
package movieclub

import (
	"context"
	"errors"
	"time"
)

const (
	Genre = "genre"

	DeliveryRetry     = "retry"
	DeliveryForbidden = "forbidden"
	DeliveryPermanent = "permanent"
	DeliveryUnknown   = "unknown"

	ResolveSent   = "sent"
	ResolveRetry  = "retry"
	ResolveCancel = "cancel"

	StatePlanned      = "planned"
	StatePollCreating = "poll_creating"
	StateOpen         = "open"
	StateClosing      = "closing"
	StateSelecting    = "selecting"
	StateReady        = "ready"
	StatePublishing   = "publishing"
	StatePublished    = "published"
	StateCancelled    = "cancelled"
	StateFailed       = "failed"
	StateUnknown      = "unknown"
)

var (
	ErrActiveRound = errors.New("movie poll already active")
	ErrConflict    = errors.New("movie poll state changed")
	ErrDuplicate   = errors.New("movie poll already planned")
	ErrDisabled    = errors.New("movie polls are not configured")
)

type Schedule struct {
	ID, ChatID, Effective int64
	Feature, Clock, Zone  string
	Weekday               int
	Enabled               bool
}

type Option struct {
	RoundID, ProviderID int64
	Position, Votes     int
	Kind, Label         string
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
	Feature, State, PollID, Winner, ResultText          string
	ErrorCode, Page2State                               string
	Options                                             []Option
}

type DeliveryError struct {
	Kind  string
	After time.Duration
}

func (e *DeliveryError) Error() string { return "movie delivery: " + e.Kind }

type Catalog interface {
	Discover(ctx context.Context, genreID int64, page, minVotes int) ([]Movie, error)
	PosterURL(path string) string
}

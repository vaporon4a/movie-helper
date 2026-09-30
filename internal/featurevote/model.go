// Package featurevote implements chat-scoped feature collection and voting.
package featurevote

import (
	"context"
	"errors"
	"time"
)

type IdeaState string
type RoundState string
type BallotMode string
type DeliveryKind string
type ResolveAction string

const (
	IdeaActive      IdeaState = "active"
	IdeaBacklog     IdeaState = "backlog"
	IdeaImplemented IdeaState = "implemented"
	IdeaRemoved     IdeaState = "removed"

	RoundPlanned    RoundState = "planned"
	RoundOpening    RoundState = "opening"
	RoundOpen       RoundState = "open"
	RoundClosing    RoundState = "closing"
	RoundReady      RoundState = "ready"
	RoundPublishing RoundState = "publishing"
	RoundPublished  RoundState = "published"
	RoundCancelled  RoundState = "cancelled"
	RoundFailed     RoundState = "failed"
	RoundUnknown    RoundState = "unknown"

	BallotPrivate BallotMode = "private"
	BallotNative  BallotMode = "native"

	DeliveryRetry     DeliveryKind = "retry"
	DeliveryForbidden DeliveryKind = "forbidden"
	DeliveryPermanent DeliveryKind = "permanent"
	DeliveryUnknown   DeliveryKind = "unknown"

	ResolveSent   ResolveAction = "sent"
	ResolveRetry  ResolveAction = "retry"
	ResolveCancel ResolveAction = "cancel"
)

const (
	DefaultRoundDuration = 24 * time.Hour
	RunoffDuration       = 12 * time.Hour
	MinRoundDuration     = 5 * time.Minute
	MaxIdeaRunes         = 1500
	MinIdeaRunes         = 20
	MaxIdeasPerWeek      = 20
	NativePollMaxOptions = 12
)

var (
	ErrConflict    = errors.New("feature vote state changed")
	ErrDuplicate   = errors.New("feature idea already exists")
	ErrRateLimit   = errors.New("feature idea rate limit")
	ErrActiveRound = errors.New("feature vote already active")
	ErrClosed      = errors.New("feature vote is closed")
	ErrNotFound    = errors.New("feature vote not found")
)

type Idea struct {
	ID, ChatID, AuthorID int64
	Text, Title, Hash    string
	State                IdeaState
	CreatedAt, UpdatedAt int64
}

type Schedule struct {
	ChatID, Effective int64
	Weekday           int
	Clock, Zone       string
	Enabled           bool
}

type Option struct {
	RoundID, IdeaID int64
	Position, Votes int
	Title, Text     string
}

type Round struct {
	ID, ChatID, SlotAt, OpenedAt, ClosesAt, NextAttempt int64
	MessageID, WinnerID, ParentID, RunoffID             int64
	Token, PollID, ErrorCode, Outcome                   string
	State                                               RoundState
	BallotMode                                          BallotMode
	Options                                             []Option
}

type OpenResult struct {
	MessageID int
	PollID    string
	Mode      BallotMode
}

type View struct {
	Round      Round
	Options    []Option
	SelectedID int64
}

type SettingsView struct {
	Schedule         Schedule
	Active, Untitled int
	Latest           []Round
	Configured       bool
}

type DeliveryError struct {
	Kind   DeliveryKind
	After  time.Duration
	Reason string
}

func (e *DeliveryError) Error() string { return "feature vote delivery: " + string(e.Kind) }

type Transport interface {
	OpenRound(context.Context, Round, string) (OpenResult, error)
	CloseRound(context.Context, Round) ([]int, error)
	SendResult(context.Context, Round) error
}

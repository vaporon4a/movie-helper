// Package clubprofile collects anonymous club observations independently of recommendations.
package clubprofile

import (
	"errors"
	"time"
)

type Topic string
type PollState string
type ResultSource string
type CleanupState string
type Aspect string

const (
	DeliveryUnknown              = "unknown"
	ResolveRetry                 = "retry"
	Genre           Topic        = "genre"
	Reference       Topic        = "reference"
	PostWatch       Topic        = "post_watch"
	Interest                     = "interest"
	LegacyInterest               = "legacy_interest"
	Enjoyment                    = "enjoyment"
	AspectResponse               = "aspect_response"
	QuestionV1                   = "club-question-v1"
	CoverageV1                   = "coverage-v1"
	DefaultDuration              = 24 * time.Hour
	CatchupWindow                = 30 * time.Minute
	Overall         Aspect       = "overall"
	Pace            Aspect       = "pace"
	Engagement      Aspect       = "engagement"
	Characters      Aspect       = "characters"
	Visuals         Aspect       = "visuals"
	Planned         PollState    = "planned"
	Creating        PollState    = "creating"
	Open            PollState    = "open"
	Closing         PollState    = "closing"
	Completed       PollState    = "completed"
	Unknown         PollState    = "unknown"
	Cancelled       PollState    = "cancelled"
	Failed          PollState    = "failed"
	SourceUpdate    ResultSource = "closed_update"
	SourceStop      ResultSource = "stop_poll"
	SourceLegacy    ResultSource = "legacy"
	CleanupPending  CleanupState = "pending"
	CleanupDeleted  CleanupState = "deleted"
	CleanupBlocked  CleanupState = "blocked"
	CleanupExpired  CleanupState = "expired"
)

var (
	ErrNotFound  = errors.New("club profile record not found")
	ErrConflict  = errors.New("club profile state changed")
	ErrInvalid   = errors.New("invalid club profile data")
	ErrDuplicate = errors.New("club profile operation already applied")
)

func (t Topic) Valid() bool { return t == Genre || t == Reference || t == PostWatch }

type Option struct {
	Key         string `json:"key"`
	Text        string `json:"text"`
	ProviderID  int64  `json:"provider_id,omitempty"`
	Metadata    string `json:"metadata,omitempty"`
	Substantive bool   `json:"substantive"`
}

type Question struct {
	Topic       Topic    `json:"topic"`
	Intent      string   `json:"intent"`
	Aspect      Aspect   `json:"aspect,omitempty"`
	Scale       string   `json:"scale"`
	Version     string   `json:"version"`
	Text        string   `json:"text"`
	Description string   `json:"description"`
	Options     []Option `json:"options"`
	Anonymous   bool     `json:"anonymous"`
	Multiple    bool     `json:"multiple"`
	Revoting    bool     `json:"revoting"`
	Source      string   `json:"source"`
}

type PollResult struct {
	PollID     string       `json:"poll_id"`
	Counts     []int        `json:"counts"`
	Total      *int         `json:"total_voter_count"`
	Closed     bool         `json:"closed"`
	Source     ResultSource `json:"source"`
	ReceivedAt time.Time    `json:"received_at"`
	UpdateID   int64        `json:"update_id,omitempty"`
}

type Poll struct {
	ID, ChatID, SessionID, BatchID, LegacyID int64
	Position                                 int
	State                                    PollState
	Question                                 Question
	TelegramID                               string
	MessageID                                int
	SlotAt, OpenedAt, ClosesAt, NextAttempt  time.Time
	Duration                                 time.Duration
	ErrorCode                                string
	Result                                   *PollResult
	CleanupEligible                          bool
	DeliveryCounted                          bool
}

type Schedule struct {
	ID, ChatID  int64
	Topic       Topic
	Weekday     int
	Clock, Zone string
	Enabled     bool
	Effective   time.Time
	WeekRoles   map[string]string
}

// ReviewPlan is frozen before the first question; repeats never pool respondents.
type ReviewPlan struct {
	Version        string   `json:"version"`
	Targets        []Aspect `json:"targets"`
	MinimumAnswers int      `json:"minimum_answers"`
	MaxQuestions   int      `json:"max_questions"`
	MaxPerBatch    int      `json:"max_per_batch"`
	MaxPerTarget   int      `json:"max_per_target"`
}

type Observation struct {
	Question Question
	Result   PollResult
}

type Coverage struct {
	Overall    bool
	Covered    map[Aspect]bool
	Sufficient bool
	Reason     string
}

type Film struct {
	TMDBID               int64
	Title, OriginalTitle string
	Year                 int
	Metadata             FilmMetadata
	Source               string
	FetchedAt            time.Time
}

type WatchEvent struct {
	ID, ChatID, TMDBID                     int64
	Date, DatePrecision, Zone, Mode, State string
	Revision                               int
	Film                                   Film
}

type ReviewSession struct {
	ID, ChatID, WatchID       int64
	Plan                      ReviewPlan
	State, Reason             string
	StartBefore, FinishBefore time.Time
}

type CleanupJob struct {
	PollID, ChatID        int64
	MessageID             int
	State                 CleanupState
	NextAttempt, Deadline time.Time
	Attempts              int
	Reason                string
}

type ProfileStatus struct {
	AutoReview    bool
	CleanupIssues []CleanupJob
}

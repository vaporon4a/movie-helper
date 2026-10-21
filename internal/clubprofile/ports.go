package clubprofile

import (
	"context"
	"time"
)

type PollRepository interface {
	PlanProfilePoll(context.Context, *int64, Poll) (int64, error)
	ProfilePolls(context.Context, int64, PollState) ([]Poll, error)
	ClaimProfilePoll(context.Context, int64, PollState, PollState) (bool, error)
	OpenProfilePoll(context.Context, int64, string, int, time.Time) error
	DeferProfilePoll(context.Context, int64, PollState, PollState, time.Time, string) error
	CompleteProfilePoll(context.Context, PollResult) (bool, error)
}

type ScheduleRepository interface {
	ProfileSchedules(context.Context, int64) ([]Schedule, error)
	SetProfileSchedule(context.Context, int64, Schedule) error
	PauseProfileSchedules(context.Context, int64, int64, Topic, time.Time) error
}

type PollTransport interface {
	OpenProfilePoll(context.Context, Poll) (string, int, error)
	CloseProfilePoll(context.Context, Poll) (PollResult, error)
}

type QuestionSource interface {
	InterestQuestion(context.Context, Topic, int64, uint64, time.Time) (Question, error)
}

type RecoveryRepository interface {
	RecoverProfilePolls(context.Context, time.Time) error
	ResolveProfilePoll(context.Context, int64, int64, int64, string, time.Time) error
}

type ReviewRepository interface {
	ReserveProfileSlot(context.Context, Schedule, Question, time.Time, time.Time) error
	AdvanceProfileReviews(context.Context, time.Time) error
}

type ProfileStatusRepository interface {
	ProfileStatus(context.Context, int64) (ProfileStatus, error)
}

type DeliveryError struct {
	Kind   string
	Reason string
	After  time.Duration
}

func (e *DeliveryError) Error() string { return "profile poll delivery: " + e.Kind }

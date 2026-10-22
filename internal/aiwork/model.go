// Package aiwork coordinates durable background AI work without owning domain payloads.
package aiwork

import "time"

const (
	FilmResearch       = "film_research"
	ClubReminderRefill = "club_reminder_refill"
	FactRefill         = "fact_refill"
	MemeRefill         = "meme_refill"
	FeatureTitle       = "feature_title"
)

const (
	PriorityBackground = 20
	PriorityNormal     = 50
	PriorityUpcoming   = 70
	PriorityLowStock   = 80
	PriorityUrgent     = 100
)

type Work struct {
	ID, ScopeID                          int64
	Kind, Key, State, LastErrorCode      string
	Priority, Attempts                   int
	NextAttempt, ClaimedUntil, UpdatedAt int64
}

type Outcome struct {
	Done   bool
	After  time.Duration
	Reason string
}

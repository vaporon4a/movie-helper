package clubprofile

import (
	"slices"
	"time"
)

const reviewPartial = "partial"

// ReviewWindow starts with the first confirmed question, not the viewing date.
const ReviewWindow = 28 * 24 * time.Hour

type ReviewDecision struct {
	State, Reason string
	Next          []Aspect
	Coverage      Coverage
}

// ReviewProgress uses independent final snapshots, never synthetic pooled respondents.
func ReviewProgress(session ReviewSession, polls []Poll, now time.Time) ReviewDecision {
	observations, attempts, count, unresolved, unknown := reviewEvidence(polls)
	d := ReviewDecision{State: session.State, Coverage: session.Plan.Coverage(observations, unresolved)}
	if session.State == reviewPartial && d.Coverage.Sufficient {
		d.State, d.Reason = "completed_sufficient", "late_final"
		return d
	}
	if session.State != "queued" && session.State != "researching" && session.State != "blocked" {
		d.Reason = session.Reason
		return d
	}
	return nextReviewDecision(d, session, polls, attempts, count, unresolved, unknown, now)
}

func nextReviewDecision(d ReviewDecision, s ReviewSession, polls []Poll, attempts map[Aspect]int, count int, unresolved, unknown bool, now time.Time) ReviewDecision {
	switch {
	case d.Coverage.Sufficient:
		d.State, d.Reason = "completed_sufficient", "coverage_met"
	case !s.FinishBefore.IsZero() && !now.Before(s.FinishBefore):
		d.State, d.Reason = reviewPartial, "deadline"
	case unknown:
		d.State, d.Reason = "blocked", "delivery_or_result_unknown"
	case unresolved:
		d.State, d.Reason = "researching", "awaiting_results"
	case count >= s.Plan.MaxQuestions:
		d.State, d.Reason = reviewPartial, "question_budget"
	case consecutiveEmptyBatches(polls) >= 2:
		d.State, d.Reason = reviewPartial, "two_empty_batches"
	default:
		d.Next = nextReviewAspects(s.Plan, d.Coverage, attempts, s.Plan.MaxQuestions-count)
		if len(d.Next) == 0 {
			d.State, d.Reason = reviewPartial, "insufficient_responses"
		} else {
			d.Reason = "ready"
		}
	}
	return d
}

func nextReviewAspects(plan ReviewPlan, coverage Coverage, attempts map[Aspect]int, remaining int) []Aspect {
	targets := append([]Aspect{Overall}, plan.Targets...)
	// Cover a new dimension before repeating a question with too few answers.
	slices.SortStableFunc(targets, func(a, b Aspect) int { return attempts[a] - attempts[b] })
	out := make([]Aspect, 0, plan.MaxPerBatch)
	for _, aspect := range targets {
		covered := coverage.Covered[aspect]
		if aspect == Overall {
			covered = coverage.Overall
		}
		if covered || attempts[aspect] >= plan.MaxPerTarget {
			continue
		}
		out = append(out, aspect)
		if len(out) >= plan.MaxPerBatch || len(out) >= remaining {
			break
		}
	}
	return out
}

func consecutiveEmptyBatches(polls []Poll) int {
	type batch struct {
		id       int64
		answers  int
		complete bool
	}
	byID := map[int64]*batch{}
	var order []int64
	for _, p := range polls {
		if p.BatchID == 0 || (p.State == Cancelled && p.MessageID == 0) || (p.State == Failed && p.MessageID == 0) {
			continue
		}
		b := byID[p.BatchID]
		if b == nil {
			b = &batch{id: p.BatchID, complete: true}
			byID[p.BatchID] = b
			order = append(order, p.BatchID)
		}
		if p.Result == nil || p.State != Completed {
			b.complete = false
			continue
		}
		b.answers += (Observation{Question: p.Question, Result: *p.Result}).Answers()
	}
	slices.Sort(order)
	n := 0
	for i := len(order) - 1; i >= 0; i-- {
		b := byID[order[i]]
		if !b.complete || b.answers > 0 {
			break
		}
		n++
	}
	return n
}

// ResearchRoles protects both ordinary topics, then spends at most half the slots on research.
func ResearchRoles(schedules []Schedule) map[int64]bool {
	ordered := slices.Clone(schedules)
	slices.SortFunc(ordered, func(a, b Schedule) int {
		if a.Weekday != b.Weekday {
			return (a.Weekday+6)%7 - (b.Weekday+6)%7
		}
		if a.Clock < b.Clock {
			return -1
		}
		if a.Clock > b.Clock {
			return 1
		}
		return int(a.ID - b.ID)
	})
	protected := protectedInterestSlots(ordered)
	n := 0
	for _, s := range ordered {
		if s.Enabled {
			n++
		}
	}
	quota := min(n/2, n-len(protected))
	roles := map[int64]bool{}
	for _, s := range ordered {
		if s.Enabled && !protected[s.ID] && quota > 0 {
			roles[s.ID] = true
			quota--
		}
	}
	return roles
}

func reviewEvidence(polls []Poll) ([]Observation, map[Aspect]int, int, bool, bool) {
	observations := make([]Observation, 0, len(polls))
	attempts := map[Aspect]int{}
	unresolved := false
	unknown := false
	count := 0
	for _, p := range polls {
		if p.Result != nil && p.State == Completed {
			observations = append(observations, Observation{Question: p.Question, Result: *p.Result})
		}
		if p.DeliveryCounted || p.MessageID > 0 || p.State == Unknown || p.State == Creating {
			count++
			attempts[p.Question.Aspect]++
		}
		if pollUnresolved(p) {
			unresolved = true
		}
		if p.State == Unknown || (p.State == Failed && p.MessageID > 0) {
			unknown = true
		}
	}
	return observations, attempts, count, unresolved, unknown
}

func pollUnresolved(p Poll) bool {
	return p.State == Planned || p.State == Creating || p.State == Unknown || p.State == Open || p.State == Closing || (p.State == Failed && p.MessageID > 0)
}

func protectedInterestSlots(ordered []Schedule) map[int64]bool {
	protected := map[int64]bool{}
	for _, s := range ordered {
		if s.Enabled && s.Topic == Reference {
			protected[s.ID] = true
			break
		}
	}
	for i := len(ordered) - 1; i >= 0; i-- {
		s := ordered[i]
		if s.Enabled && s.Topic == Genre {
			protected[s.ID] = true
			break
		}
	}
	return protected
}

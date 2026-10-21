package clubprofile

import (
	"testing"
	"time"
)

func TestResearchSlotPresentationRespectsFrozenWeek(t *testing.T) {
	day := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	schedules := []Schedule{{ID: 1, Topic: Genre, Weekday: 1, Clock: "09:00", Enabled: true, Zone: "UTC", WeekRoles: map[string]string{"2026-10-19": "ordinary"}}, {ID: 2, Topic: Genre, Weekday: 3, Clock: "09:00", Enabled: true, Zone: "UTC", WeekRoles: map[string]string{"2026-10-19": "research"}}, {ID: 3, Topic: Reference, Weekday: 3, Clock: "12:00", Enabled: true, Zone: "UTC"}}
	if !ResearchSlot(schedules, schedules[1], day) {
		t.Fatal("lost frozen research role")
	}
	if ResearchSlot(schedules, schedules[2], day) {
		t.Fatal("new slot steals frozen research quota")
	}
	if ResearchSlot(schedules, schedules[1], day.AddDate(0, 0, 7)) {
		t.Fatal("next week did not protect ordinary genre")
	}
}

func TestLateFinalCanCompletePartialWithoutRestart(t *testing.T) {
	now := time.Now()
	s := ReviewSession{State: "partial", Reason: "deadline", Plan: DefaultReviewPlan(), FinishBefore: now.Add(-time.Hour)}
	polls := make([]Poll, 0, 5)
	for i, a := range []Aspect{Overall, Pace, Engagement, Characters, Visuals} {
		q, _ := ReviewQuestion(a, "Фильм")
		counts := make([]int, len(q.Options))
		counts[0] = 3
		polls = append(polls, Poll{State: Completed, MessageID: i + 1, Question: q, Result: &PollResult{PollID: "p", Counts: counts, Total: new(3), Closed: true, Source: SourceUpdate, ReceivedAt: now}})
	}
	d := ReviewProgress(s, polls, now)
	if d.State != "completed_sufficient" || len(d.Next) != 0 {
		t.Fatal(d)
	}
	s.State = "cancelled"
	d = ReviewProgress(s, polls, now)
	if d.State != "cancelled" {
		t.Fatal("cancelled review resumed")
	}
}

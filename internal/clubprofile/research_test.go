package clubprofile

import (
	"reflect"
	"testing"
	"time"
)

func researchQuestions(t *testing.T) []Question {
	t.Helper()
	qs := make([]Question, 0, 4)
	for _, family := range []string{"investigation_interest_v1", "synthetic_food_credibility_v1", "crowded_city_atmosphere_enjoyment_v1", "environmental_crisis_credibility_v1"} {
		text := "Насколько правдоподобно показано устройство мира?"
		if family == "investigation_interest_v1" {
			text = "Насколько вам интересно расследование?"
		}
		if family == "crowded_city_atmosphere_enjoyment_v1" {
			text = "Насколько вам понравилась атмосфера города?"
		}
		q, err := ResearchQuestion(family, text, "Предмет измерения", 603, 1, "dossier", []EvidenceReference{{SourceID: "source", SpanID: "text"}})
		if err != nil {
			t.Fatal(err)
		}
		qs = append(qs, q)
	}
	return qs
}
func TestResearchCoverageAndRepeatBudget(t *testing.T) {
	qs := researchQuestions(t)
	plan, err := ResearchReviewPlan("bundle", qs)
	if err != nil {
		t.Fatal(err)
	}
	overall, err := ReviewQuestion(Overall, "Матрица")
	if err != nil {
		t.Fatal(err)
	}
	observations := make([]Observation, 0, 1+len(qs))
	polls := make([]Poll, 0, 1+len(qs))
	for i, q := range append([]Question{overall}, qs...) {
		counts := make([]int, len(q.Options))
		counts[0] = 3
		result := PollResult{PollID: "p", Counts: counts, Total: new(3), Closed: true, Source: SourceStop, ReceivedAt: time.Now()}
		observations = append(observations, Observation{q, result})
		polls = append(polls, Poll{ID: int64(i + 1), Question: q, Result: &result, State: Completed, DeliveryCounted: true})
	}
	if !plan.Coverage(observations, false).Sufficient {
		t.Fatal("not covered")
	}
	observations[1].Result.Counts[0] = 2
	observations[1].Result.Total = new(2)
	polls[1].Result = &observations[1].Result
	d := ReviewProgress(ReviewSession{Plan: plan, State: "researching"}, polls, time.Now())
	if len(d.Next) != 1 || d.Next[0] != qs[0].PlanTarget() {
		t.Fatal("repeat target", d)
	}
	unrelated := qs[0]
	unrelated.Text = "Иное измерение?"
	observations[1].Question = unrelated
	if plan.Coverage(observations, false).Sufficient {
		t.Fatal("foreign wording covered plan")
	}
}
func TestResearchProjectionCanonicalEqualFilmsAndQuarantine(t *testing.T) {
	qs := researchQuestions(t)
	plan, err := ResearchReviewPlan("bundle", qs)
	if err != nil {
		t.Fatal(err)
	}
	watches := []WatchEvent{{ID: 1, ChatID: -1, TMDBID: 603, Date: "2026-10-01", State: "active", Revision: 1}, {ID: 2, ChatID: -1, TMDBID: 603, Date: "2026-10-10", State: "active", Revision: 1}, {ID: 3, ChatID: -1, TMDBID: 604, Date: "2026-10-02", State: "active", Revision: 1}}
	other := researchQuestions(t)
	for i := range other {
		other[i].Measurement.TMDBID = 604
	}
	planOther, err := ResearchReviewPlan("other", other)
	if err != nil {
		t.Fatal(err)
	}
	sessions := []ReviewSession{{ID: 1, ChatID: -1, WatchID: 1, Plan: plan}, {ID: 2, ChatID: -1, WatchID: 2, Plan: plan}, {ID: 3, ChatID: -1, WatchID: 3, Plan: planOther}}
	makePoll := func(id, session int64, q Question, index, n int) Poll {
		counts := make([]int, len(q.Options))
		counts[index] = n
		return Poll{ID: id, ChatID: -1, SessionID: session, TelegramID: "p", State: Completed, Question: q, ClosesAt: time.Unix(id, 0), Result: &PollResult{PollID: "p", Total: new(n), Counts: counts, Closed: true, ReceivedAt: time.Unix(id, 0), Source: SourceStop}}
	}
	polls := []Poll{makePoll(1, 1, qs[0], 0, 100), makePoll(2, 2, qs[0], 0, 3), makePoll(3, 2, qs[0], 1, 50), makePoll(4, 3, other[0], 1, 3), makePoll(5, 3, other[1], 0, 2)}
	result := ProjectResearch(watches, sessions, polls)
	if len(result.Families) != 1 || result.Families[0].Films != 2 || result.Families[0].Answers != 6 || result.Provisional != 1 || !reflect.DeepEqual(result.CanonicalPollIDs, []int64{2, 4}) {
		t.Fatal(result)
	}
	if result.Families[0].Shares[qs[0].Options[0].Key] != 0.5 {
		t.Fatal("unequal film weight", result)
	}
	if len(result.Evidence) != 2 || result.Evidence[0].PollID != 2 || result.Evidence[0].WatchID != 2 || result.Evidence[1].PollID != 4 || result.Evidence[1].TMDBID != 604 {
		t.Fatal("wrong evidence selection", result.Evidence)
	}
	evidenceShare := float64(0)
	for _, e := range result.Evidence {
		for _, option := range e.Options {
			if option.Key == qs[0].Options[0].Key {
				evidenceShare += float64(option.Count) / float64(e.Answers)
			}
		}
	}
	evidenceShare /= float64(len(result.Evidence))
	if evidenceShare != result.Families[0].Shares[qs[0].Options[0].Key] {
		t.Fatal("evidence disagrees with summary")
	}
	reversed := []Poll{polls[4], polls[3], polls[2], polls[1], polls[0]}
	if !reflect.DeepEqual(result, ProjectResearch(watches, sessions, reversed)) {
		t.Fatal("unstable rebuild")
	}
	polls[1].CleanupEligible = true
	polls[1].ErrorCode = "cleanup_retry"
	polls[1].NextAttempt = time.Now()
	if !reflect.DeepEqual(result, ProjectResearch(watches, sessions, polls)) {
		t.Fatal("transport cleanup changed analytical evidence")
	}
	watches[1].Revision = 2
	changed := ProjectResearch(watches, sessions, polls)
	if changed.Quarantined != 2 {
		t.Fatal(changed)
	}
}

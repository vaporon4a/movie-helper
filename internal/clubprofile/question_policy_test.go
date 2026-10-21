package clubprofile

import (
	"testing"
	"time"
)

func observation(t *testing.T, a Aspect, counts []int) Observation {
	t.Helper()
	q, err := ReviewQuestion(a, "Матрица")
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, n := range counts {
		total += n
	}
	return Observation{Question: q, Result: PollResult{PollID: "p", Counts: counts, Total: &total, Closed: true, Source: SourceUpdate, ReceivedAt: time.Now()}}
}

func TestCoverageKeepsNeutralAndDoesNotPoolRepeats(t *testing.T) {
	p := DefaultReviewPlan()
	obs := make([]Observation, 0, 3+len(p.Targets))
	obs = append(obs, []Observation{observation(t, Overall, []int{0, 0, 3, 0, 0, 2, 0}), observation(t, Pace, []int{1, 0, 1, 5, 0}), observation(t, Pace, []int{1, 0, 1, 0, 0})}...)
	c := p.Coverage(obs, false)
	if !c.Overall || c.Covered[Pace] || c.Sufficient {
		t.Fatal(c)
	}
	for _, a := range p.Targets {
		obs = append(obs, observation(t, a, []int{2, 0, 1, 0, 0}))
	}
	if c = p.Coverage(obs, false); !c.Sufficient {
		t.Fatal(c)
	}
	if c = p.Coverage(obs, true); c.Sufficient || c.Reason != "awaiting_final" {
		t.Fatal(c)
	}
	obs[len(obs)-1].Question.Scale = "different-scale"
	if p.Coverage(obs, false).Sufficient {
		t.Fatal("incompatible scale counted")
	}
}

func TestFinalSnapshotValidation(t *testing.T) {
	o := observation(t, Overall, []int{1, 0, 0, 0, 2, 0, 1})
	if err := o.Result.ValidateFinal(o.Question, "p"); err != nil {
		t.Fatal(err)
	}
	for _, modify := range []func(*PollResult){func(r *PollResult) { r.Total = nil }, func(r *PollResult) { r.Total = new(0) }, func(r *PollResult) { r.Closed = false }, func(r *PollResult) { r.Counts = []int{1} }, func(r *PollResult) { r.Counts[0] = -1 }, func(r *PollResult) { r.Source = SourceLegacy }, func(r *PollResult) { r.PollID = "other" }} {
		r := o.Result
		r.Counts = append([]int(nil), o.Result.Counts...)
		modify(&r)
		if r.ValidateFinal(o.Question, "p") == nil {
			t.Fatal("accepted incomplete result", r)
		}
	}
	z := observation(t, Overall, []int{0, 0, 0, 0, 0, 0, 0})
	if err := z.Result.ValidateFinal(z.Question, "p"); err != nil {
		t.Fatal("real zero rejected", err)
	}
}

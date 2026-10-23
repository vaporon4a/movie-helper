package clubprofile

import (
	"slices"
	"strings"
	"testing"
)

func TestPreparationReadinessAndAdoptionAreDifferent(t *testing.T) {
	p := PreparationState{Stage: "ready", WatchRevision: 2, PreparedRevision: 2, EnrollmentMode: "on", EffectiveMode: "on", Approved: true}
	s := ReviewSession{}
	if !strings.Contains(p.Label(s), "ждём") {
		t.Fatal(p.Label(s))
	}
	s.Plan.Version = CoverageV2
	p.EffectiveMode = "off"
	if !strings.Contains(p.Label(s), "план принят") {
		t.Fatal("enrollment switch hid adopted plan")
	}
	s.Plan.Version = CoverageV1
	p.Stage = "fallback"
	if !strings.Contains(p.Label(s), "обычная серия") {
		t.Fatal("late AI replaced fallback")
	}
}
func TestReviewQueuePrioritizesResearchThenID(t *testing.T) {
	in := []ReviewSession{{ID: 1, State: "queued"}, {ID: 4, State: "researching"}, {ID: 2, State: "researching"}, {ID: 3, State: "blocked"}, {ID: 0, State: "cancelled"}}
	q := ReviewQueue(in)
	ids := make([]int64, 0, len(q))
	for _, s := range q {
		ids = append(ids, s.ID)
	}
	if !slices.Equal(ids, []int64{2, 3, 4, 1}) || in[0].ID != 1 {
		t.Fatal(ids)
	}
}

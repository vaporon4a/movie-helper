package storage

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/vaporon4a/movie-helper/internal/clubprofile"
	"github.com/vaporon4a/movie-helper/internal/filmresearch"
)

func approvedBundle(t *testing.T, s *Store) filmresearch.Bundle {
	t.Helper()
	b := researchBundle(t, s)
	for i, q := range b.Preparation.Questions {
		text := map[string]string{"interest": "Насколько вам интересно расследование?", "enjoyment": "Насколько вам понравилась атмосфера города?", "credibility": "Насколько правдоподобно показана система?"}[q.Measurement.MeasurementID]
		next, err := clubprofile.ResearchQuestion(q.Measurement.FamilyID, text, q.Measurement.Context, q.Measurement.TMDBID, 1, q.Measurement.DossierID, q.Measurement.Evidence)
		must(t, err)
		b.Preparation.Questions[i] = next
	}
	return b
}
func approvedAudit() filmresearch.Audit {
	a := filmresearch.Audit{Provenance: filmresearch.Provenance{Provider: "fixture", Model: "fixture", PromptVersion: "audit-v1", ValidatorVersion: "v1"}}
	for i := range 4 {
		a.Checks = append(a.Checks, filmresearch.Check{Index: i, Quote: "Синтетический источник для проверки контракта", Rationale: "Синтетическое основание для проверки связности.", Supported: true, Neutral: true, SingleConstruct: true, SpoilerFree: true, Clear: true})
	}
	return a
}
func prepareResearch(t *testing.T, s *Store, b filmresearch.Bundle) {
	t.Helper()
	ctx := t.Context()
	j, err := s.ResearchJob(ctx, b.Preparation.SessionID)
	must(t, err)
	j.Bundle = b
	must(t, s.CheckpointResearch(ctx, j, "generate", testNow))
	j, err = s.ResearchJob(ctx, j.SessionID)
	must(t, err)
	must(t, s.CheckpointResearch(ctx, j, "audit", testNow))
	j, err = s.ResearchJob(ctx, j.SessionID)
	must(t, err)
	j.Audit = approvedAudit()
	must(t, s.CheckpointResearch(ctx, j, "ready", testNow))

}
func researchSchedule(t *testing.T, s *Store) (clubprofile.Schedule, clubprofile.Question) {
	t.Helper()
	ctx := t.Context()
	must(t, s.SetZone(ctx, 800, -1, "UTC", testNow.Add(-time.Hour)))
	for i, sc := range []clubprofile.Schedule{
		{ChatID: -1, Topic: clubprofile.Genre, Weekday: 1, Clock: "09:00", Enabled: true, Effective: testNow.Add(-time.Hour)},
		{ChatID: -1, Topic: clubprofile.Reference, Weekday: 1, Clock: "12:00", Enabled: true, Effective: testNow.Add(-time.Hour)},
		{ChatID: -1, Topic: clubprofile.Genre, Weekday: 3, Clock: "09:00", Enabled: true, Effective: testNow.Add(-time.Hour)},
		{ChatID: -1, Topic: clubprofile.Reference, Weekday: 3, Clock: "12:00", Enabled: true, Effective: testNow.Add(-time.Hour)},
	} {
		must(t, s.SetProfileSchedule(ctx, int64(801+i), sc))
	}
	schedules, err := s.ProfileSchedules(ctx, -1)
	must(t, err)
	q := clubprofile.Question{Topic: clubprofile.Genre, Intent: clubprofile.Interest, Version: clubprofile.QuestionV1, Scale: "single-interest-v1", Text: "Жанр?", Anonymous: true, Options: []clubprofile.Option{{Key: "a", Text: "Первый", Substantive: true}, {Key: "b", Text: "Второй", Substantive: true}}}
	return schedules[0], q
}
func TestResearchReadyFreezesWithFirstBatch(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	must(t, s.ConfigureFilmResearch(ctx, "on", map[int64]bool{-1: true}))
	b := approvedBundle(t, s)
	prepareResearch(t, s, b)
	sc, q := researchSchedule(t, s)
	must(t, s.ReserveProfileSlot(ctx, sc, q, testNow, testNow))
	sessions, err := s.ProfileSessions(ctx, -1)
	must(t, err)
	if sessions[0].Plan.Version != clubprofile.CoverageV2 || !sessions[0].FinishBefore.IsZero() {
		t.Fatal("wrong freeze or premature clock", sessions)
	}
	polls, err := s.ProfilePolls(ctx, -1, clubprofile.Planned)
	must(t, err)
	if len(polls) != 2 || polls[0].Question.Aspect != clubprofile.Overall || polls[1].Question.Version != clubprofile.QuestionV2 {
		t.Fatal(polls)
	}
	for _, p := range polls {
		claimed, e := s.ClaimProfilePoll(ctx, p.ID, clubprofile.Planned, clubprofile.Creating)
		must(t, e)
		if !claimed {
			t.Fatal("claim")
		}
		must(t, s.OpenProfilePoll(ctx, p.ID, fmt.Sprint(p.ID), int(p.ID), testNow))
	}
	sessions, err = s.ProfileSessions(ctx, -1)
	must(t, err)
	if !sessions[0].FinishBefore.Equal(testNow.Add(clubprofile.ReviewWindow)) {
		t.Fatal("clock", sessions)
	}
	if _, err = s.ResearchJob(ctx, b.Preparation.SessionID); !errors.Is(err, filmresearch.ErrConflict) {
		t.Fatal("late job active", err)
	}
}
func TestResearchFallbackAndShadowDoNotAdoptLateBundle(t *testing.T) {
	for _, mode := range []string{"on", "shadow"} {
		t.Run(mode, func(t *testing.T) {
			s := testStore(t)
			ctx := t.Context()
			must(t, s.ConfigureFilmResearch(ctx, mode, map[int64]bool{-1: true}))
			b := approvedBundle(t, s)
			if mode == "shadow" {
				prepareResearch(t, s, b)
			}
			sc, q := researchSchedule(t, s)
			must(t, s.ReserveProfileSlot(ctx, sc, q, testNow, testNow))
			sessions, err := s.ProfileSessions(ctx, -1)
			must(t, err)
			if sessions[0].Plan.Version != clubprofile.CoverageV1 {
				t.Fatal("fallback changed")
			}
			_, err = s.SaveResearchPreparation(ctx, b, testNow)
			if mode == "on" && !errors.Is(err, filmresearch.ErrConflict) {
				t.Fatal("late snapshot adopted", err)
			}
		})
	}
}
func TestResearchRejectsStaleAuditAndChangedViewing(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	must(t, s.ConfigureFilmResearch(ctx, "on", map[int64]bool{-1: true}))
	b := approvedBundle(t, s)
	j, err := s.ResearchJob(ctx, b.Preparation.SessionID)
	must(t, err)
	j.Bundle = b
	must(t, s.CheckpointResearch(ctx, j, "generate", testNow))
	if err = s.CheckpointResearch(ctx, j, "generate", testNow); !errors.Is(err, filmresearch.ErrConflict) {
		t.Fatal("stale checkpoint", err)
	}
	j, err = s.ResearchJob(ctx, j.SessionID)
	must(t, err)
	must(t, s.CheckpointResearch(ctx, j, "audit", testNow))
	j, err = s.ResearchJob(ctx, j.SessionID)
	must(t, err)
	j.Audit = approvedAudit()
	j.Bundle.Preparation.Questions[0].Text = "Насколько вам интересно новое расследование?"
	if err = s.CheckpointResearch(ctx, j, "ready", testNow); !errors.Is(err, filmresearch.ErrConflict) {
		t.Fatal("audit not bound to bundle", err)
	}
	must(t, s.ChangeProfileWatch(ctx, 900, -1, j.WatchID, "annul", "", testNow))
	if _, err = s.ResearchJob(ctx, j.SessionID); !errors.Is(err, filmresearch.ErrConflict) {
		t.Fatal("cancelled job", err)
	}
}
func TestResearchSeedsOnlyTwoAndDoesNotResurrectDone(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	must(t, s.EnsureChat(ctx, -1))
	must(t, s.ConfigureFilmResearch(ctx, "on", map[int64]bool{-1: true}))
	for i := range 3 {
		addProfileWatch(t, s, -1, int64(100+i), testNow.AddDate(0, 0, -i).Format(time.DateOnly), testNow)
	}
	must(t, s.SeedAIWork(ctx, testNow))
	var count int
	must(t, s.db.QueryRowContext(ctx, "SELECT count(*) FROM ai_work WHERE kind='film_research'").Scan(&count))
	if count != 2 {
		t.Fatal(count)
	}
	_, err := s.db.ExecContext(ctx, "UPDATE ai_work SET state='done' WHERE kind='film_research'")
	must(t, err)
	must(t, s.SeedAIWork(ctx, testNow.Add(time.Hour)))
	must(t, s.db.QueryRowContext(ctx, "SELECT count(*) FROM ai_work WHERE kind='film_research' AND state='done'").Scan(&count))
	if count != 2 {
		t.Fatal("done reset", count)
	}
}
func TestResearchOffDoesNotEnrollExistingViewings(t *testing.T) {
	s := testStore(t)
	b := researchBundle(t, s)
	ctx := t.Context()
	must(t, s.ConfigureFilmResearch(ctx, "on", map[int64]bool{-1: true}))
	must(t, s.SeedAIWork(ctx, testNow))
	var n int
	must(t, s.db.QueryRowContext(ctx, "SELECT count(*) FROM film_research_jobs WHERE session_id=?", b.Preparation.SessionID).Scan(&n))
	if n != 0 {
		t.Fatal("old session enrolled")
	}
}

func TestResearchDisableRetiresPendingWindow(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	must(t, s.ConfigureFilmResearch(ctx, "on", map[int64]bool{-1: true}))
	b := approvedBundle(t, s)
	must(t, s.SeedAIWork(ctx, testNow))
	must(t, s.ConfigureFilmResearch(ctx, "off", map[int64]bool{-1: true}))
	var stage string
	must(t, s.db.QueryRowContext(ctx, "SELECT stage FROM film_research_jobs WHERE session_id=?", b.Preparation.SessionID).Scan(&stage))
	if stage != "superseded" {
		t.Fatal(stage)
	}
	must(t, s.ConfigureFilmResearch(ctx, "on", map[int64]bool{-1: true}))
	addProfileWatch(t, s, -1, 930, testNow.AddDate(0, 0, -1).Format(time.DateOnly), testNow)
	must(t, s.SeedAIWork(ctx, testNow))
	var n int
	must(t, s.db.QueryRowContext(ctx, "SELECT count(*) FROM ai_work WHERE kind='film_research'").Scan(&n))
	if n != 2 {
		t.Fatal("new viewing blocked by old job", n)
	}
}

func TestResearchQuestionRevisionMustMatchPreparation(t *testing.T) {
	s := testStore(t)
	b := approvedBundle(t, s)
	must(t, b.Validate())
	b.Preparation.Questions[0].Measurement.WatchRevision++
	if _, err := s.SaveResearchPreparation(t.Context(), b, testNow); !errors.Is(err, filmresearch.ErrInvalid) {
		t.Fatal("accepted stale question descriptor", err)
	}
}

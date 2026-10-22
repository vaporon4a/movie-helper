package content

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/vaporon4a/movie-helper/internal/ai"
	"github.com/vaporon4a/movie-helper/internal/aiwork"
	"github.com/vaporon4a/movie-helper/internal/clubprofile"
	"github.com/vaporon4a/movie-helper/internal/filmresearch"
	"github.com/vaporon4a/movie-helper/internal/groq"
)

type researchMemory struct {
	job   filmresearch.Job
	steps []string
}

func (m *researchMemory) ResearchJob(context.Context, int64) (filmresearch.Job, error) {
	return m.job, nil
}
func (m *researchMemory) CheckpointResearch(_ context.Context, j filmresearch.Job, next string, _ time.Time) error {
	if j.Version != m.job.Version {
		return filmresearch.ErrConflict
	}
	j.Version++
	j.Stage = next
	m.job = j
	m.steps = append(m.steps, next)
	return nil
}

type researchSourceStub struct{}

func (researchSourceStub) ResearchSources(_ context.Context, f clubprofile.Film, now time.Time) ([]filmresearch.SourceSnapshot, error) {
	text := "Синтетическая завязка о расследовании, искусственной пище и экологическом кризисе в переполненном городе."
	return []filmresearch.SourceSnapshot{{Version: filmresearch.SourceV1, TMDBID: f.TMDBID, Kind: "plot", URL: "https://example.org/film", Language: "ru", Attribution: "fixture", Text: text, Spans: []filmresearch.EvidenceSpan{{ID: "text", End: len(text)}}, FetchedAt: now}}, nil
}

type researchGenerator struct {
	calls   int
	reject  bool
	failure error
}

func (g *researchGenerator) Generate(_ context.Context, instruction string, parts []ai.Part) (ai.Selection, error) {
	g.calls++
	if g.failure != nil {
		return ai.Selection{}, g.failure
	}
	var in filmresearch.Input
	if err := json.Unmarshal([]byte(parts[0].Text), &in); err != nil {
		return ai.Selection{}, err
	}
	var value any
	if instruction == filmresearch.AuditPrompt {
		a := filmresearch.Audit{}
		for i := range 4 {
			a.Checks = append(a.Checks, filmresearch.Check{Index: i, Quote: in.Sources[0].Text, Rationale: "Синтетическая проверка предмета.", Supported: !g.reject, Neutral: true, SingleConstruct: true, SpoilerFree: true, Clear: true})
		}
		value = a
	} else {
		p := filmresearch.Proposal{}
		for _, f := range []string{"investigation_interest_v1", "synthetic_food_credibility_v1", "crowded_city_atmosphere_enjoyment_v1", "environmental_crisis_credibility_v1"} {
			text := "Насколько правдоподобно показано устройство мира?"
			if f == "investigation_interest_v1" {
				text = "Насколько вам интересно расследование?"
			}
			if f == "crowded_city_atmosphere_enjoyment_v1" {
				text = "Насколько вам понравилась атмосфера города?"
			}
			p.Questions = append(p.Questions, filmresearch.Candidate{Family: f, Text: text, Context: "Синтетический контекст", Evidence: []clubprofile.EvidenceReference{{SourceID: in.Sources[0].ID, SpanID: "text"}}})
		}
		value = p
	}
	raw, err := json.Marshal(value)
	return ai.Selection{Index: new(0), Structured: raw}, err
}
func researchJobFixture() filmresearch.Job {
	return filmresearch.Job{SessionID: 1, ChatID: -1, WatchID: 1, WatchRevision: 1, Film: clubprofile.Film{TMDBID: 603, Title: "Матрица", Year: 1999}, Stage: "sources", Version: 1}
}
func TestResearchBackgroundCheckpointsSurviveRestart(t *testing.T) {
	store := &researchMemory{job: researchJobFixture()}
	g := &researchGenerator{}
	for step := range 3 {
		r := ResearchPreparer{Store: store, Sources: []filmresearch.Source{researchSourceStub{}}, Primary: g}
		out, err := r.Execute(t.Context(), aiwork.Work{ScopeID: 1, Attempts: step + 1}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if out.Done != (step == 2) {
			t.Fatal(out)
		}
	}
	if store.job.Stage != "ready" || g.calls != 2 || !store.job.Audit.Accepted() {
		t.Fatal(store.job.Stage, g.calls)
	}
	if err := store.job.Bundle.Validate(); err != nil {
		t.Fatal(err)
	}
}
func TestResearchProviderCooldownIsDurable(t *testing.T) {
	store := &researchMemory{job: researchJobFixture()}
	source := ResearchPreparer{Store: store, Sources: []filmresearch.Source{researchSourceStub{}}}
	if _, err := source.Execute(t.Context(), aiwork.Work{ScopeID: 1, Attempts: 1}, time.Now()); err != nil {
		t.Fatal(err)
	}
	g := &researchGenerator{failure: &groq.HTTPError{Status: 429, After: 7 * time.Minute}}
	r := ResearchPreparer{Store: store, Primary: g}
	out, err := r.Execute(t.Context(), aiwork.Work{ScopeID: 1, Attempts: 2}, time.Now())
	if err != nil || out.After != 7*time.Minute || store.job.Stage != "generate" || g.calls != 1 {
		t.Fatal(out, err)
	}
	g.failure = ai.ErrDailyLimit
	out, err = r.Execute(t.Context(), aiwork.Work{ScopeID: 1, Attempts: 3}, time.Now())
	if err != nil || out.Reason != "local_daily_limit" {
		t.Fatal(out, err)
	}
}
func TestResearchOneRepairThenFallback(t *testing.T) {
	store := &researchMemory{job: researchJobFixture()}
	g := &researchGenerator{}
	r := ResearchPreparer{Store: store, Sources: []filmresearch.Source{researchSourceStub{}}, Primary: g}
	for step := range 2 {
		if _, err := r.Execute(t.Context(), aiwork.Work{ScopeID: 1, Attempts: step + 1}, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	g.reject = true
	out, err := r.Execute(t.Context(), aiwork.Work{ScopeID: 1, Attempts: 3}, time.Now())
	if err != nil || out.Done || store.job.Repairs != 1 || store.job.Stage != "generate" {
		t.Fatal(out, err)
	}
	// Malformed repair must not create an endless generation loop.
	g.failure = filmresearch.ErrInvalid
	out, err = r.Execute(t.Context(), aiwork.Work{ScopeID: 1, Attempts: 4}, time.Now())
	if err != nil || !out.Done || store.job.Stage != "rejected" {
		t.Fatal(out, err)
	}
	if !errors.Is(g.failure, filmresearch.ErrInvalid) {
		t.Fatal("fixture")
	}
}

func TestResearchRepairCanProduceAcceptedBundle(t *testing.T) {
	store := &researchMemory{job: researchJobFixture()}
	g := &researchGenerator{}
	r := ResearchPreparer{Store: store, Sources: []filmresearch.Source{researchSourceStub{}}, Primary: g}
	for step := range 2 {
		if _, err := r.Execute(t.Context(), aiwork.Work{ScopeID: 1, Attempts: step + 1}, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	g.reject = true
	if _, err := r.Execute(t.Context(), aiwork.Work{ScopeID: 1, Attempts: 3}, time.Now()); err != nil {
		t.Fatal(err)
	}
	g.reject = false
	for step := range 2 {
		if _, err := r.Execute(t.Context(), aiwork.Work{ScopeID: 1, Attempts: 4 + step}, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if store.job.Stage != "ready" || store.job.Repairs != 1 || g.calls != 4 {
		t.Fatal(store.job.Stage, store.job.Repairs, g.calls)
	}
}

func TestResearchFallbackWaitsForTwoPrimaryDeferrals(t *testing.T) {
	store := &researchMemory{job: researchJobFixture()}
	setup := ResearchPreparer{Store: store, Sources: []filmresearch.Source{researchSourceStub{}}}
	if _, err := setup.Execute(t.Context(), aiwork.Work{ScopeID: 1, Attempts: 1}, time.Now()); err != nil {
		t.Fatal(err)
	}
	primary := &researchGenerator{failure: &groq.HTTPError{Status: 503, After: time.Minute}}
	secondary := &researchGenerator{}
	r := ResearchPreparer{Store: store, Primary: primary, Secondary: secondary}
	reason := ""
	for step := range 3 {
		out, err := r.Execute(t.Context(), aiwork.Work{ScopeID: 1, Attempts: step + 2, LastErrorCode: reason}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		reason = out.Reason
		if step < 2 && secondary.calls != 0 {
			t.Fatal("early fallback")
		}
	}
	if primary.calls != 2 || secondary.calls != 1 || store.job.Stage != "audit" {
		t.Fatal(primary.calls, secondary.calls, store.job.Stage)
	}
}

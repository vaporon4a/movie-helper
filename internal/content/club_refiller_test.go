package content

import (
	"context"
	"github.com/vaporon4a/movie-helper/internal/ai"
	"github.com/vaporon4a/movie-helper/internal/aiwork"
	"github.com/vaporon4a/movie-helper/internal/clubreminder"
	"github.com/vaporon4a/movie-helper/internal/gemini"
	"github.com/vaporon4a/movie-helper/internal/groq"
	"testing"
	"time"
)

type reminderGenerator struct {
	calls       int
	result      ai.Selection
	err         error
	instruction string
}

func (g *reminderGenerator) Generate(_ context.Context, instruction string, _ []ai.Part) (ai.Selection, error) {
	g.calls++
	g.instruction = instruction
	return g.result, g.err
}
func TestClubRefillerDeferredPrimaryDoesNotUseFallback(t *testing.T) {
	first := &reminderGenerator{err: &gemini.HTTPError{Status: 503, Deferred: true, After: 3 * time.Minute}}
	second := &reminderGenerator{}
	r := ClubRefiller{Primary: first, Secondary: second}
	if _, err := r.generate(context.Background(), clubreminder.Explore, "roast", nil, false); err == nil {
		t.Fatal("missing failure")
	}
	if second.calls != 0 {
		t.Fatal("used fallback before retry-after")
	}
}
func TestClubRefillerFallbackAndTextValidation(t *testing.T) {
	text := clubreminder.Fallback(clubreminder.Explore, "roast")
	index := 0
	first := &reminderGenerator{result: ai.Selection{Index: &index, Text: "too short"}}
	second := &reminderGenerator{result: ai.Selection{Index: &index, Text: text.Body}}
	r := ClubRefiller{Primary: first, Secondary: second}
	result, err := r.generate(context.Background(), clubreminder.Explore, "roast", nil, false)
	if err != nil || result.Provider != "groq" || result.Tone != "roast" {
		t.Fatal(result, err)
	}
}
func TestClubRequestIntentToneAndStock(t *testing.T) {
	texts := make([]clubreminder.Text, 0, 6)
	texts = append(texts, []clubreminder.Text{{Intent: clubreminder.Explore, Tone: "friendly", State: "ready"}, {Intent: clubreminder.Explore, Tone: "dramatic", State: "ready"}, {Intent: clubreminder.Explore, Tone: "sarcastic", State: "ready"}, {Intent: clubreminder.Explore, Tone: "roast", State: "used"}}...)
	intent, tone, full := clubRequest(texts)
	if intent != clubreminder.Discuss || full || tone == "friendly" || tone == "dramatic" || tone == "sarcastic" {
		t.Fatal(intent, tone, full)
	}
	texts = append(texts, clubreminder.Text{Intent: clubreminder.Discuss, State: "ready"}, clubreminder.Text{Intent: clubreminder.Discuss, State: "ready"})
	_, _, full = clubRequest(texts)
	if !full {
		t.Fatal("stock not capped")
	}
}

type reminderRepoStub struct{}

func (reminderRepoStub) ClubSettings(context.Context, int64, time.Time) (clubreminder.Settings, error) {
	return clubreminder.Settings{Enabled: true}, nil
}
func (reminderRepoStub) ClubTexts(context.Context, int64, time.Time) ([]clubreminder.Text, error) {
	return nil, nil
}
func (reminderRepoStub) SaveClubText(context.Context, int64, clubreminder.Text, time.Time) (bool, error) {
	return true, nil
}

func TestClubRefillerUsesReserveAfterThreeDurableDeferrals(t *testing.T) {
	first := &reminderGenerator{err: &gemini.HTTPError{Status: 503, Deferred: true, After: 3 * time.Minute}}
	index := 0
	second := &reminderGenerator{result: ai.Selection{Index: &index, Text: clubreminder.Fallback(clubreminder.Explore, "friendly").Body}}
	r := ClubRefiller{Store: reminderRepoStub{}, Primary: first, Secondary: second}
	work := aiwork.Work{ScopeID: -1}
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	for n := 1; n <= 5; n++ {
		work.Attempts = n
		outcome, err := r.Execute(context.Background(), work, now)
		if err != nil {
			t.Fatal(err)
		}
		if n <= 3 {
			if second.calls != 0 || primaryDeferrals(outcome.Reason) != n || outcome.After != 3*time.Minute {
				t.Fatal(n, outcome, second.calls)
			}
		} else if first.calls != 3 || second.calls != n-3 || outcome.Reason != reminderFallbackMode {
			t.Fatal(n, outcome, first.calls, second.calls)
		}
		// A different process gets only the persisted work reason on the next execution.
		work.LastErrorCode = outcome.Reason
		now = now.Add(outcome.After)
	}
}

func TestSecondaryDeferralDoesNotCountAsGeminiDeferral(t *testing.T) {
	first := &reminderGenerator{err: &ai.ValidationError{Reason: "invalid"}}
	second := &reminderGenerator{err: &groq.HTTPError{Status: 429, Deferred: true, After: time.Minute}}
	r := ClubRefiller{Store: reminderRepoStub{}, Primary: first, Secondary: second}
	outcome, err := r.Execute(context.Background(), aiwork.Work{ScopeID: -1}, time.Now())
	if err != nil || primaryDeferrals(outcome.Reason) != 0 || outcome.After != time.Minute {
		t.Fatal(outcome, err)
	}
}

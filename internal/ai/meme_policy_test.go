package ai

import (
	"context"
	"encoding/json"
	"github.com/vaporon4a/movie-helper/internal/daily"
	"strings"
	"testing"
)

func TestMemeAssessmentGateAndIndependentRanking(t *testing.T) {
	reviews := []Review{
		{Index: new(0), Reason: "eligible", Meme: assessment(3, 5, 5)}, // model's favorite stays reserve
		{Index: new(1), Reason: "eligible", Meme: assessment(4, 4, 2)}, // a simple pun can publish
		{Index: new(2), Reason: "eligible", Meme: assessment(5, 4, 4)},
		{Index: new(3), Reason: "context_required", Meme: assessment(5, 5, 5)},
	}
	selection := Selection{Index: new(0), Reviews: reviews}
	if err := ValidateMemeSelection(selection, 4); err != nil {
		t.Fatal(err)
	}
	got, err := acceptedMemes(selection, []daily.Item{{Key: "reserve"}, {Key: "pun"}, {Key: "best"}, {Key: "missing-context"}})
	if err != nil || len(got) != 2 || got[0].Key != "best" || got[1].Key != "pun" {
		t.Fatal(got, err)
	}
	for _, item := range got {
		var audit memeAudit
		if err = json.Unmarshal([]byte(item.SourceEvidence), &audit); err != nil || audit.Policy != daily.MemePolicy || audit.Decision != "publish" || item.GenerationPolicy != daily.MemePolicy {
			t.Fatal(item, err)
		}
	}
	if MemeDecision(reviews[0]) != "reserve" || MemeDecision(reviews[3]) != "reject" {
		t.Fatal("unsafe gate")
	}
	// Equal scores must not depend on map iteration or the model's chosen index.
	selection = Selection{Index: new(1), Reviews: []Review{reviews[1], {Index: new(0), Reason: "eligible", Meme: assessment(4, 4, 2)}}}
	got, err = acceptedMemes(selection, []daily.Item{{Key: "a"}, {Key: "b"}})
	if err != nil || len(got) != 2 || got[0].Key != "a" {
		t.Fatal(got, err)
	}
}
func TestMemeAssessmentRejectsIncompleteAndInventedOutputs(t *testing.T) {
	good := decision("accepted")
	for _, mutate := range []func(*Selection){
		func(s *Selection) { s.Text = "invented caption" },
		func(s *Selection) { s.Evidence = "invented quote" },
		func(s *Selection) { s.Reviews[0].Meme = nil },
		func(s *Selection) { s.Reviews[0].Meme.Humor = 0 },
		func(s *Selection) { s.Reviews[0].Meme.Clarity = 6 },

		func(s *Selection) { s.Reviews[0].Reason = "accepted" },
	} {
		candidate := decision("accepted")
		mutate(&candidate)
		if ValidateMemeSelection(candidate, 1) == nil {
			t.Fatal("unsafe assessment accepted", candidate)
		}
	}
	if err := ValidateMemeSelection(good, 1); err != nil {
		t.Fatal(err)
	}
}

func TestWeakFormatsCannotBypassPublishGateWithHighScores(t *testing.T) {
	for _, kind := range []string{"typo", "caption", "none"} {
		r := Review{Reason: "eligible", Meme: assessment(5, 5, 5)}
		r.Meme.JokeType = kind
		if MemeDecision(r) == "publish" {
			t.Fatal("weak format published", kind)
		}
	}
	r := Review{Reason: "eligible", Meme: assessment(4, 4, 2)}
	r.Meme.JokeType = "wordplay"
	if MemeDecision(r) != "publish" {
		t.Fatal("simple readable pun lost")
	}
}

func TestInvalidExplanationDoesNotPoisonValidNeighbour(t *testing.T) {
	broken := Review{Index: new(0), Reason: "eligible", Meme: assessment(5, 5, 5)}
	broken.Meme.Punchline = strings.Repeat("я", 1000)
	valid := Review{Index: new(1), Reason: "eligible", Meme: assessment(4, 4, 4)}
	selection := Selection{Index: new(0), Reviews: []Review{broken, valid}}
	if err := ValidateMemeSelection(selection, 2); err != nil {
		t.Fatal(err)
	}
	got, err := acceptedMemes(selection, []daily.Item{{Key: "broken"}, {Key: "good"}})
	if err != nil || len(got) != 1 || got[0].Key != "good" {
		t.Fatal(got, err)
	}
	cache := reviewCache{}
	editor := &Editor{Reviews: cache, Scope: "gemini:test:" + MemeReviewVersion}
	if err := editor.memeSession().saveReviews(context.Background(), selection, []daily.Item{{Key: "broken"}, {Key: "good"}}, 1); err != nil {
		t.Fatal(err)
	}
	if len(cache) != 0 {
		t.Fatal("broken provider output poisoned source cache", cache)
	}
	broken.Meme.Punchline = " "
	if MemeDecision(broken) != "reject" {
		t.Fatal("ungrounded publication")
	}
}

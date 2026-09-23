package content

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/vaporon4a/movie-helper/internal/ai"
	"github.com/vaporon4a/movie-helper/internal/daily"
	"github.com/vaporon4a/movie-helper/internal/gemini"
	"github.com/vaporon4a/movie-helper/internal/groq"
)

type editor struct {
	reject bool
	err    error
}

func TestNormalizePreviewError(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantCode   string
		wantStatus int
	}{
		{"validation", &ai.ValidationError{Reason: "bad response"}, "invalid_ai_selection", 0},
		{"daily limit", ai.ErrDailyLimit, "local_daily_limit", 0},
		{"groq quota", &groq.HTTPError{Status: 429}, "groq_quota", 429},
		{"gemini model", &gemini.HTTPError{Status: 404}, "gemini_model_unavailable", 404},
		{"wrapped unavailable", fmt.Errorf("request: %w", &gemini.HTTPError{Status: 503}), "gemini_unavailable", 503},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := errors.AsType[*daily.PreviewError](normalizePreviewError(test.err))
			if !ok || got.Code != test.wantCode || got.Status != test.wantStatus {
				t.Fatalf("got %#v, want code %q status %d", got, test.wantCode, test.wantStatus)
			}
		})
	}

	unknown := errors.New("offline")
	if got := normalizePreviewError(unknown); !errors.Is(got, unknown) {
		t.Fatalf("unknown error was replaced: %v", got)
	}
}

func (e editor) SelectMeme(_ context.Context, items []daily.Item) (*daily.Item, error) {
	if e.reject || e.err != nil || len(items) == 0 {
		return nil, e.err
	}
	return &items[0], nil
}
func (e editor) Fact(context.Context, []gemini.Article) (*daily.Item, error) { return nil, e.err }

func TestAutomaticCandidatesRequireGeminiApproval(t *testing.T) {
	for _, tc := range []struct {
		name    string
		ed      Editor
		wantErr bool
	}{
		{"missing", nil, false}, {"rejected", editor{reject: true}, false}, {"failed", editor{err: errors.New("offline")}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &Provider{Memes: memes{{Key: "new"}}, History: history{}, Editor: tc.ed}
			got, err := p.Candidates(context.Background(), daily.Meme, -1)
			if len(got) != 0 || (err != nil) != tc.wantErr {
				t.Fatal("unapproved candidate escaped", got, err)
			}
		})
	}
}

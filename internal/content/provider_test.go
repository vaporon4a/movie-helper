package content

import (
	"context"
	"errors"
	"testing"

	"github.com/vaporon4a/movie-helper/internal/daily"
	"github.com/vaporon4a/movie-helper/internal/gemini"
)

type editor struct {
	reject bool
	err    error
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

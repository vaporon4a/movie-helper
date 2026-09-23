// Package content combines independent sources with optional AI selection.
package content

import (
	"context"
	"errors"

	"github.com/vaporon4a/movie-helper/internal/ai"
	"github.com/vaporon4a/movie-helper/internal/daily"
	"github.com/vaporon4a/movie-helper/internal/gemini"
	"github.com/vaporon4a/movie-helper/internal/groq"
)

type Memes interface {
	Candidates(context.Context) ([]daily.Item, error)
}
type Editor interface {
	SelectMeme(context.Context, []daily.Item) (*daily.Item, error)
	Fact(context.Context, []gemini.Article) (*daily.Item, error)
}
type History interface {
	Seen(context.Context, int64, string, string) (bool, error)
}
type Provider struct {
	Memes   Memes
	Editor  Editor
	History History
	Facts   *Wikipedia
}

func (p *Provider) Candidates(ctx context.Context, kind string, chat int64) (items []daily.Item, err error) {
	defer func() {
		err = normalizePreviewError(err)
	}()
	// Automatic candidates must always pass AI selection.
	if p.Editor == nil {
		return nil, nil
	}
	if kind == daily.Meme {
		items, err := p.Memes.Candidates(ctx)
		if err != nil {
			return nil, err
		}
		var unseen []daily.Item
		for _, i := range items {
			seen, err := p.History.Seen(ctx, chat, kind, i.Key)
			if err != nil {
				return nil, err
			}
			if !seen {
				unseen = append(unseen, i)
			}
		}
		i, err := p.Editor.SelectMeme(ctx, unseen)
		if err != nil {
			return nil, err
		}
		if i == nil {
			return nil, nil
		}
		return []daily.Item{*i}, nil
	}
	var articles []gemini.Article
	if p.Facts != nil {
		var err error
		articles, err = p.Facts.Articles(ctx, chat, p.History)
		if err != nil {
			return nil, err
		}
	}

	i, err := p.Editor.Fact(ctx, articles)
	if err != nil {
		return nil, err
	}
	if i == nil {
		return nil, nil
	}
	return []daily.Item{*i}, nil
}

func normalizePreviewError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := errors.AsType[*ai.ValidationError](err); ok {
		return &daily.PreviewError{Code: "invalid_ai_selection"}
	}
	if errors.Is(err, ai.ErrDailyLimit) {
		return &daily.PreviewError{Code: "local_daily_limit"}
	}
	if status, ok := errors.AsType[*groq.HTTPError](err); ok {
		return &daily.PreviewError{Code: previewStatusCode("groq", status.Status), Status: status.Status}
	}
	if status, ok := errors.AsType[*gemini.HTTPError](err); ok {
		return &daily.PreviewError{Code: previewStatusCode("gemini", status.Status), Status: status.Status}
	}
	return err
}

func previewStatusCode(provider string, status int) string {
	suffix := "http"
	switch status {
	case 401, 403:
		suffix = "access_denied"
	case 404:
		suffix = "model_unavailable"
	case 429:
		suffix = "quota"
	case 503:
		suffix = "unavailable"
	}
	return provider + "_" + suffix
}

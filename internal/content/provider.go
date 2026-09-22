// Package content combines independent sources with optional AI selection.
package content

import (
	"context"

	"github.com/vaporon4a/movie-helper/internal/daily"
	"github.com/vaporon4a/movie-helper/internal/gemini"
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

func (p *Provider) Candidates(ctx context.Context, kind string, chat int64) ([]daily.Item, error) {
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

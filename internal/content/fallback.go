package content

import (
	"context"
	"log/slog"
	"time"

	"github.com/vaporon4a/movie-helper/internal/daily"
	"github.com/vaporon4a/movie-helper/internal/gemini"
)

// Fallback gives each provider a bounded time slot. A valid rejection is final;
// only failures fall through, and no unselected material can be returned.
type Fallback struct {
	Primary, Secondary Editor
	AttemptTimeout     time.Duration
	Log                *slog.Logger
}

func (f *Fallback) selectItem(ctx context.Context, call func(context.Context, Editor) (*daily.Item, error)) (*daily.Item, error) {
	timeout := f.AttemptTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	var last error
	for n, e := range []Editor{f.Primary, f.Secondary} {
		if e == nil {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		attempt, cancel := context.WithTimeout(ctx, timeout)
		item, err := call(attempt, e)
		cancel()
		if err == nil {
			return item, nil
		}
		last = err
		if f.Log != nil {
			f.Log.Warn("AI provider attempt failed", "provider", []string{"gemini", "groq"}[n])
		}
	}
	return nil, last
}
func (f *Fallback) SelectMeme(ctx context.Context, items []daily.Item) (*daily.Item, error) {
	return f.selectItem(ctx, func(ctx context.Context, e Editor) (*daily.Item, error) { return e.SelectMeme(ctx, items) })
}
func (f *Fallback) Fact(ctx context.Context, articles []gemini.Article) (*daily.Item, error) {
	return f.selectItem(ctx, func(ctx context.Context, e Editor) (*daily.Item, error) { return e.Fact(ctx, articles) })
}

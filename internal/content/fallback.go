package content

import (
	"context"
	"errors"
	"fmt"
	"github.com/vaporon4a/movie-helper/internal/ai"
	"github.com/vaporon4a/movie-helper/internal/groq"
	"log/slog"
	"time"

	"github.com/vaporon4a/movie-helper/internal/daily"
	"github.com/vaporon4a/movie-helper/internal/gemini"
)

// Fallback gives each provider a bounded time slot. A valid rejection is final;
// only failures fall through, and no unselected material can be returned.
type Fallback struct {
	Primary, Secondary               Editor
	PrimaryTimeout, SecondaryTimeout time.Duration
	Log                              *slog.Logger
}

func (f *Fallback) selectItem(ctx context.Context, call func(context.Context, Editor) (*daily.Item, error)) (*daily.Item, error) {
	var last error
	for n, e := range []Editor{f.Primary, f.Secondary} {
		if e == nil {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		timeout := f.PrimaryTimeout
		if timeout <= 0 {
			timeout = GeminiSelectionTimeout
		}
		if n == 1 {
			timeout = f.SecondaryTimeout
			if timeout <= 0 {
				timeout = GroqSelectionTimeout
			}
		}
		attempt, cancel := context.WithTimeout(ctx, timeout)
		item, err := call(attempt, e)
		cancel()
		if err == nil {
			return item, nil
		}
		last = err
		if f.Log != nil {
			f.Log.Warn("AI provider attempt failed", "provider", []string{"gemini", "groq"}[n], "reason", failureReason(err))
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

// Only emit codes created by our adapters, never upstream bodies or URLs.
func failureReason(err error) string {
	var validation *ai.ValidationError
	if errors.As(err, &validation) {
		return "invalid_selection:" + validation.Reason
	}
	var g *gemini.HTTPError
	if errors.As(err, &g) {
		return fmt.Sprintf("http_%d", g.Status)
	}
	var q *groq.HTTPError
	if errors.As(err, &q) {
		return fmt.Sprintf("http_%d", q.Status)
	}
	if errors.Is(err, ai.ErrDailyLimit) {
		return "daily_limit"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	return "source_or_connection_failed"
}

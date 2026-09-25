package content

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vaporon4a/movie-helper/internal/ai"
	"github.com/vaporon4a/movie-helper/internal/daily"
	"github.com/vaporon4a/movie-helper/internal/gemini"
)

type fakeEditor struct {
	calls        int
	err          error
	reject, wait bool
	remaining    time.Duration
}

func (e *fakeEditor) SelectMeme(ctx context.Context, _ []daily.Item) (*daily.Item, error) {
	return e.selectItem(ctx)
}
func (e *fakeEditor) SelectMemes(ctx context.Context, _ []daily.Item, _ int) ([]daily.Item, error) {
	item, err := e.selectItem(ctx)
	if err != nil || item == nil {
		return nil, err
	}
	return []daily.Item{*item}, nil
}
func (e *fakeEditor) Fact(ctx context.Context, _ []gemini.Article) (*daily.Item, error) {
	return e.selectItem(ctx)
}
func (e *fakeEditor) selectItem(ctx context.Context) (*daily.Item, error) {
	e.calls++
	if deadline, ok := ctx.Deadline(); ok {
		e.remaining = time.Until(deadline)
	}
	if e.wait {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if e.err != nil || e.reject {
		return nil, e.err
	}
	return &daily.Item{Key: "selected"}, nil
}

func TestProviderDeadlinesLeaveTimeForFallback(t *testing.T) {
	primary := &fakeEditor{err: errors.New("upstream unavailable")}
	secondary := &fakeEditor{}
	f := &Fallback{Primary: primary, Secondary: secondary}
	ctx, cancel := context.WithTimeout(context.Background(), FetchTimeout)
	defer cancel()
	item, err := f.SelectMeme(ctx, nil)
	if err != nil || item == nil || primary.remaining < 149*time.Second || secondary.remaining < 59*time.Second {
		t.Fatal("provider deadline truncated", err, primary.remaining, secondary.remaining)
	}
}

func TestFailureReasonDoesNotExposeUpstreamDetails(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{errors.New("secret upstream body and URL"), "source_or_connection_failed"},
		{&gemini.HTTPError{Status: 503}, "http_503"},
		{&ai.ValidationError{Reason: "meme_review_count_or_index"}, "invalid_selection:meme_review_count_or_index"},
		{ai.ErrDailyLimit, "daily_limit"},
	} {
		if got := failureReason(tc.err); got != tc.want {
			t.Fatal(got)
		}
	}
}
func TestFallbackErrorsLimitsAndTimeout(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		wait bool
	}{{"503", &gemini.HTTPError{Status: 503}, false}, {"budget", ai.ErrDailyLimit, false}, {"malformed", errors.New("invalid selection"), false}, {"timeout", nil, true}} {
		for _, kind := range []string{"meme", "fact"} {
			t.Run(tc.name+kind, func(t *testing.T) {
				primary := &fakeEditor{err: tc.err, wait: tc.wait}
				secondary := &fakeEditor{}
				f := &Fallback{Primary: primary, Secondary: secondary, PrimaryTimeout: 5 * time.Millisecond, SecondaryTimeout: 5 * time.Millisecond}
				var item *daily.Item
				var err error
				if kind == "meme" {
					item, err = f.SelectMeme(context.Background(), nil)
				} else {
					item, err = f.Fact(context.Background(), nil)
				}
				if err != nil || item == nil || primary.calls != 1 || secondary.calls != 1 {
					t.Fatal(item, err, primary.calls, secondary.calls)
				}
			})
		}
	}
}
func TestFallbackHonorsRejectionAndParentCancellation(t *testing.T) {
	secondary := &fakeEditor{}
	f := &Fallback{Primary: &fakeEditor{reject: true}, Secondary: secondary}
	item, err := f.SelectMeme(context.Background(), nil)
	if err != nil || item != nil || secondary.calls != 0 {
		t.Fatal("rejection bypassed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = f.Fact(ctx, nil)
	if !errors.Is(err, context.Canceled) || secondary.calls != 0 {
		t.Fatal("cancellation ignored")
	}
	f.Primary = nil
	item, err = f.Fact(context.Background(), nil)
	if err != nil || item == nil || secondary.calls != 1 {
		t.Fatal("standalone fallback failed")
	}
	secondary.err = ai.ErrDailyLimit
	_, err = f.Fact(context.Background(), nil)
	if !errors.Is(err, ai.ErrDailyLimit) {
		t.Fatal("last error lost")
	}
}

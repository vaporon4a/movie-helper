// Package groq implements a separate AI provider for source selection.
package groq

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/vaporon4a/movie-helper/internal/ai"
	"github.com/vaporon4a/movie-helper/internal/daily"
)

const (
	defaultRetryDelay = time.Minute
	inlineRetryLimit  = 10 * time.Second
	deferredRetryCap  = 5 * time.Minute
)

// HTTPError deliberately exposes only safe response metadata. Upstream bodies,
// request URLs and credentials must never be attached to this error.
type HTTPError struct {
	Status      int
	After       time.Duration
	LimitKind   string
	RetrySource string
	Deferred    bool
}

func (e *HTTPError) Error() string { return fmt.Sprintf("Groq status %d", e.Status) }

type Client struct {
	HTTP                *http.Client
	BaseURL, Key, Model string
	Budget              ai.Budget
	DailyLimit          int
	Now                 func() time.Time
	Reviews             ai.ReviewCache
	Log                 *slog.Logger
	RetryDelay          time.Duration
	InlineRetryLimit    time.Duration
	MaxRetryAfter       time.Duration
	Wait                func(context.Context, time.Duration) error
}

func (c *Client) Generate(ctx context.Context, instruction string, parts []ai.Part) (ai.Selection, error) {
	var result ai.Selection
	if err := ctx.Err(); err != nil {
		return result, err
	}
	content := make([]any, 0, len(parts))
	for _, p := range parts {
		if p.Text != "" {
			content = append(content, map[string]any{"type": "text", "text": p.Text})
		}
		if p.Inline != nil {
			content = append(content, map[string]any{"type": "image_url", "image_url": map[string]string{"url": "data:" + p.Inline.MIME + ";base64," + p.Inline.Data}})
		}
	}
	body := map[string]any{
		"model": c.Model, "reasoning_effort": "none", "max_completion_tokens": 768,
		"response_format": map[string]any{"type": "json_schema", "json_schema": map[string]any{
			"name": "content_selection", "strict": true, "schema": ai.SelectionSchema(parts),
		}},
		"messages": []any{
			map[string]any{"role": "system", "content": instruction + ai.SourceInstruction},
			map[string]any{"role": "user", "content": content},
		},
	}
	data, err := json.Marshal(body)
	if err != nil {
		return result, errors.New("cannot encode Groq request")
	}
	// Never follow a redirect with API credentials, even with a custom client.
	client := *c.HTTP
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	var r *http.Response
	for attempt := 1; attempt <= 2; attempt++ {
		allowed, budgetErr := c.Budget.AllowAPI(ctx, c.Now().UTC().Format("2006-01-02"), c.DailyLimit)
		if budgetErr != nil {
			return result, errors.New("groq budget unavailable")
		}
		if !allowed {
			return result, ai.ErrDailyLimit
		}
		req, requestErr := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(data))
		if requestErr != nil {
			return result, errors.New("invalid Groq endpoint")
		}
		req.Header.Set("Authorization", "Bearer "+c.Key)
		req.Header.Set("Content-Type", "application/json")
		r, err = client.Do(req)
		if err != nil {
			return result, errors.New("groq connection failed")
		}
		if r.StatusCode == http.StatusOK {
			break
		}
		status := r.StatusCode
		_ = r.Body.Close()
		if status != http.StatusTooManyRequests {
			return result, &HTTPError{Status: status}
		}
		after, source := c.retryAfter(r.Header.Get("Retry-After"))
		httpErr := &HTTPError{Status: status, After: after, LimitKind: rateLimitKind(r.Header), RetrySource: source}
		if attempt == 2 {
			httpErr.Deferred = true
			c.logRateLimit(attempt, httpErr, false)
			return result, httpErr
		}
		if after > c.inlineLimit() || !c.waitFits(ctx, after) {
			httpErr.Deferred = true
			c.logRateLimit(attempt, httpErr, false)
			return result, httpErr
		}
		c.logRateLimit(attempt, httpErr, true)
		if err = c.wait(ctx, after); err != nil {
			return result, err
		}
	}
	defer r.Body.Close()
	var response struct {
		Choices []struct {
			Finish  string `json:"finish_reason"`
			Message struct {
				Content string `json:"content"`
				Refusal string `json:"refusal"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&response); err != nil {
		return result, &ai.ValidationError{Reason: "groq_invalid_response_json"}
	}
	if len(response.Choices) != 1 || response.Choices[0].Finish != "stop" || response.Choices[0].Message.Refusal != "" {
		return result, &ai.ValidationError{Reason: "groq_incomplete_selection"}
	}
	if err = json.Unmarshal([]byte(response.Choices[0].Message.Content), &result); err != nil || result.Index == nil {
		return result, &ai.ValidationError{Reason: "groq_invalid_selection_json"}
	}
	return result, nil
}

func (c *Client) retryAfter(value string) (time.Duration, string) {
	delay := c.RetryDelay
	if delay <= 0 {
		delay = defaultRetryDelay
	}
	source := "default"
	if seconds, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && seconds >= 0 {
		delay, source = time.Duration(seconds)*time.Second, "header_seconds"
	} else if at, err := http.ParseTime(value); err == nil {
		delay, source = at.Sub(c.Now()), "header_date"
	}
	if delay < 0 {
		delay = 0
	}
	maximum := c.MaxRetryAfter
	if maximum <= 0 {
		maximum = deferredRetryCap
	}
	if delay > maximum {
		delay, source = maximum, source+"_capped"
	}
	return delay, source
}

func (c *Client) inlineLimit() time.Duration {
	if c.InlineRetryLimit > 0 {
		return c.InlineRetryLimit
	}
	return inlineRetryLimit
}

func (c *Client) waitFits(ctx context.Context, delay time.Duration) bool {
	deadline, ok := ctx.Deadline()
	return !ok || c.Now().Add(delay).Before(deadline)
}

func (c *Client) wait(ctx context.Context, delay time.Duration) error {
	if c.Wait != nil {
		return c.Wait(ctx, delay)
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (c *Client) logRateLimit(attempt int, problem *HTTPError, retry bool) {
	if c.Log == nil {
		return
	}
	c.Log.Warn("Groq rate limit reached", "provider", "groq", "status", problem.Status, "attempt", attempt,
		"retry", retry, "retry_after_ms", problem.After.Milliseconds(), "retry_source", problem.RetrySource,
		"limit_kind", problem.LimitKind)
}

func rateLimitKind(header http.Header) string {
	tokens := strings.TrimSpace(header.Get("x-ratelimit-remaining-tokens"))
	requests := strings.TrimSpace(header.Get("x-ratelimit-remaining-requests"))
	if tokens == "0" && requests == "0" {
		return "tokens_and_requests"
	}
	if tokens == "0" {
		return "tokens"
	}
	if requests == "0" {
		return "requests"
	}
	return "unknown"
}

func (c *Client) SelectMeme(ctx context.Context, items []daily.Item) (*daily.Item, error) {
	selected, err := c.SelectMemes(ctx, items, 1)
	if err != nil || len(selected) == 0 {
		return nil, err
	}
	return &selected[0], nil
}
func (c *Client) SelectMemes(ctx context.Context, items []daily.Item, limit int) ([]daily.Item, error) {
	// Qwen accepts three images per request. Reviewing one candidate from each
	// default source together stays within the free per-minute token allowance.
	e := &ai.Editor{HTTP: c.HTTP, Generator: c, Reviews: c.Reviews, Scope: "groq:" + c.Model + ":" + ai.MemeReviewVersion, Now: c.Now, MaxBatches: 1, MaxImages: 3}
	return e.SelectMemes(ctx, items, limit)
}
func (c *Client) Fact(ctx context.Context, articles []ai.Article) (*daily.Item, error) {
	e := &ai.Editor{HTTP: c.HTTP, Generator: c, Reviews: c.Reviews, Scope: "groq:" + c.Model + ":" + ai.FactGenerationPolicy, Now: c.Now, MaxBatches: 2, MaxImages: 1, Log: c.Log}
	return e.Fact(ctx, articles)
}

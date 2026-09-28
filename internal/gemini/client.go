// Package gemini implements source selection through the Gemini API.
package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/vaporon4a/movie-helper/internal/ai"
	"github.com/vaporon4a/movie-helper/internal/daily"
)

type Budget = ai.Budget

var ErrDailyLimit = ai.ErrDailyLimit

// HTTPError deliberately excludes response bodies, credentials and request URLs.
type HTTPError struct{ Status int }

func (e *HTTPError) Error() string { return fmt.Sprintf("Gemini status %d", e.Status) }

type Client struct {
	HTTP                *http.Client
	BaseURL, Key, Model string
	Budget              Budget
	DailyLimit          int
	Now                 func() time.Time
	Reviews             ai.ReviewCache
	Log                 *slog.Logger
	RetryDelay          time.Duration
	MaxRetryAfter       time.Duration
	Wait                func(context.Context, time.Duration) error
}
type Article = ai.Article

func (c *Client) Generate(ctx context.Context, instruction string, parts []ai.Part) (ai.Selection, error) {
	var result ai.Selection
	body := map[string]any{
		"systemInstruction": map[string]any{"parts": []ai.Part{{Text: instruction + ai.SourceInstruction}}},
		"contents":          []any{map[string]any{"role": "user", "parts": parts}},
		"generationConfig":  map[string]any{"maxOutputTokens": 4096, "responseMimeType": "application/json", "responseJsonSchema": ai.SelectionSchema(parts)},
	}
	data, err := json.Marshal(body)
	if err != nil {
		return result, errors.New("cannot encode Gemini request")
	}
	var r *http.Response
	for attempt := 1; attempt <= 2; attempt++ {
		allowed, budgetErr := c.Budget.AllowAPI(ctx, c.Now().UTC().Format("2006-01-02"), c.DailyLimit)
		if budgetErr != nil {
			return result, errors.New("gemini budget unavailable")
		}
		if !allowed {
			return result, ErrDailyLimit
		}
		req, requestErr := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/models/"+url.PathEscape(c.Model)+":generateContent", bytes.NewReader(data))
		if requestErr != nil {
			return result, errors.New("invalid Gemini endpoint")
		}
		req.Header.Set("x-goog-api-key", c.Key)
		req.Header.Set("Content-Type", "application/json")
		r, err = c.HTTP.Do(req)
		if err != nil {
			return result, errors.New("gemini connection failed")
		}
		if r.StatusCode == http.StatusOK {
			break
		}
		status := r.StatusCode
		delay := c.retryAfter(r.Header.Get("Retry-After"))
		_ = r.Body.Close()
		if attempt == 2 || !temporaryStatus(status) {
			return result, &HTTPError{Status: status}
		}
		if c.Log != nil {
			c.Log.Warn("Gemini request will retry", "provider", "gemini", "status", status, "attempt", attempt, "delay_ms", delay.Milliseconds())
		}
		if err = c.wait(ctx, delay); err != nil {
			return result, err
		}
	}
	defer r.Body.Close()
	var response struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text    string `json:"text"`
					Thought bool   `json:"thought"`
				} `json:"parts"`
			} `json:"content"`
			Finish string `json:"finishReason"`
		} `json:"candidates"`
	}
	if err = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&response); err != nil {
		return result, &ai.ValidationError{Reason: "gemini_invalid_response_json"}
	}
	if len(response.Candidates) != 1 || response.Candidates[0].Finish != "STOP" {
		return result, &ai.ValidationError{Reason: "gemini_incomplete_selection"}
	}
	var text strings.Builder
	for _, p := range response.Candidates[0].Content.Parts {
		if !p.Thought {
			text.WriteString(p.Text)
		}
	}
	if err = json.Unmarshal([]byte(text.String()), &result); err != nil || result.Index == nil {
		return result, &ai.ValidationError{Reason: "gemini_invalid_selection_json"}
	}
	return result, nil
}

func temporaryStatus(status int) bool {
	return status == http.StatusBadGateway || status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout
}

func (c *Client) retryAfter(value string) time.Duration {
	base := c.RetryDelay
	if base <= 0 {
		base = 2 * time.Second
	}
	maximum := c.MaxRetryAfter
	if maximum <= 0 {
		maximum = 10 * time.Second
	}
	delay := base
	if seconds, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && seconds >= 0 {
		delay = time.Duration(seconds) * time.Second
	} else if at, err := http.ParseTime(value); err == nil {
		delay = at.Sub(c.Now())
	}
	if delay < 0 {
		delay = 0
	}
	return min(delay, maximum)
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

func (c *Client) SelectMeme(ctx context.Context, items []daily.Item) (*daily.Item, error) {
	selected, err := c.SelectMemes(ctx, items, 1)
	if err != nil || len(selected) == 0 {
		return nil, err
	}
	return &selected[0], nil
}
func (c *Client) SelectMemes(ctx context.Context, items []daily.Item, limit int) ([]daily.Item, error) {
	e := &ai.Editor{HTTP: c.HTTP, Generator: c, Reviews: c.Reviews, Scope: "gemini:" + c.Model + ":" + ai.MemeReviewVersion, Now: c.Now, MaxBatches: 2, MaxImages: 4}
	return e.SelectMemes(ctx, items, limit)
}
func (c *Client) Fact(ctx context.Context, articles []Article) (*daily.Item, error) {
	e := &ai.Editor{HTTP: c.HTTP, Generator: c, Reviews: c.Reviews, Scope: "gemini:" + c.Model + ":" + ai.FactGenerationPolicy, Now: c.Now, MaxBatches: 2, MaxImages: 4, Log: c.Log}
	return e.Fact(ctx, articles)
}

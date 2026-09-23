// Package gemini implements source selection through the Gemini API.
package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
}
type Article = ai.Article

func (c *Client) Generate(ctx context.Context, instruction string, parts []ai.Part) (ai.Selection, error) {
	var result ai.Selection
	allowed, err := c.Budget.AllowAPI(ctx, c.Now().UTC().Format("2006-01-02"), c.DailyLimit)
	if err != nil {
		return result, errors.New("gemini budget unavailable")
	}
	if !allowed {
		return result, ErrDailyLimit
	}
	body := map[string]any{
		"systemInstruction": map[string]any{"parts": []ai.Part{{Text: instruction + ai.SourceInstruction}}},
		"contents":          []any{map[string]any{"role": "user", "parts": parts}},
		"generationConfig":  map[string]any{"maxOutputTokens": 4096, "responseMimeType": "application/json", "responseJsonSchema": ai.SelectionSchema(parts)},
	}
	data, err := json.Marshal(body)
	if err != nil {
		return result, errors.New("cannot encode Gemini request")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/models/"+url.PathEscape(c.Model)+":generateContent", bytes.NewReader(data))
	if err != nil {
		return result, errors.New("invalid Gemini endpoint")
	}
	req.Header.Set("x-goog-api-key", c.Key)
	req.Header.Set("Content-Type", "application/json")
	r, err := c.HTTP.Do(req)
	if err != nil {
		return result, errors.New("gemini connection failed")
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return result, &HTTPError{Status: r.StatusCode}
	}
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

func (c *Client) SelectMeme(ctx context.Context, items []daily.Item) (*daily.Item, error) {
	e := &ai.Editor{HTTP: c.HTTP, Generator: c, Reviews: c.Reviews, Scope: "gemini:" + c.Model + ":" + ai.MemeReviewVersion, Now: c.Now, MaxBatches: 2, MaxImages: 4}
	return e.SelectMeme(ctx, items)
}
func (c *Client) Fact(ctx context.Context, articles []Article) (*daily.Item, error) {
	e := &ai.Editor{HTTP: c.HTTP, Generator: c, Reviews: c.Reviews, Scope: "gemini:" + c.Model + ":" + ai.MemeReviewVersion, Now: c.Now, MaxBatches: 2, MaxImages: 4}
	return e.Fact(ctx, articles)
}

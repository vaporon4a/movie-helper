// Package groq implements a separate AI provider for source selection.
package groq

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/vaporon4a/movie-helper/internal/ai"
	"github.com/vaporon4a/movie-helper/internal/daily"
)

type HTTPError struct{ Status int }

func (e *HTTPError) Error() string { return fmt.Sprintf("Groq status %d", e.Status) }

type Client struct {
	HTTP                *http.Client
	BaseURL, Key, Model string
	Budget              ai.Budget
	DailyLimit          int
	Now                 func() time.Time
}

func (c *Client) Generate(ctx context.Context, instruction string, parts []ai.Part) (ai.Selection, error) {
	var result ai.Selection
	if err := ctx.Err(); err != nil {
		return result, err
	}
	allowed, err := c.Budget.AllowAPI(ctx, c.Now().UTC().Format("2006-01-02"), c.DailyLimit)
	if err != nil {
		return result, errors.New("Groq budget unavailable")
	}
	if !allowed {
		return result, ai.ErrDailyLimit
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
		"model": c.Model, "reasoning_effort": "none", "max_completion_tokens": 512,
		"response_format": map[string]string{"type": "json_object"},
		"messages": []any{
			map[string]any{"role": "system", "content": instruction + ai.SourceInstruction},
			map[string]any{"role": "user", "content": content},
		},
	}
	data, err := json.Marshal(body)
	if err != nil {
		return result, errors.New("cannot encode Groq request")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(data))
	if err != nil {
		return result, errors.New("invalid Groq endpoint")
	}
	req.Header.Set("Authorization", "Bearer "+c.Key)
	req.Header.Set("Content-Type", "application/json")
	// Never follow a redirect with API credentials, even with a custom client.
	client := *c.HTTP
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	r, err := client.Do(req)
	if err != nil {
		return result, errors.New("Groq connection failed")
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return result, &HTTPError{Status: r.StatusCode}
	}
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
		return result, errors.New("invalid Groq response")
	}
	if len(response.Choices) != 1 || response.Choices[0].Finish != "stop" || response.Choices[0].Message.Refusal != "" {
		return result, errors.New("Groq did not finish a selection")
	}
	if err = json.Unmarshal([]byte(response.Choices[0].Message.Content), &result); err != nil || result.Index == nil {
		return result, errors.New("invalid Groq selection")
	}
	return result, nil
}

func (c *Client) SelectMeme(ctx context.Context, items []daily.Item) (*daily.Item, error) {
	// Two images leave room for the prompt and output within the free 8k TPM limit.
	e := &ai.Editor{HTTP: c.HTTP, Generator: c, MaxImages: 2}
	return e.SelectMeme(ctx, items)
}
func (c *Client) Fact(ctx context.Context, articles []ai.Article) (*daily.Item, error) {
	e := &ai.Editor{HTTP: c.HTTP, Generator: c, MaxImages: 2}
	return e.Fact(ctx, articles)
}

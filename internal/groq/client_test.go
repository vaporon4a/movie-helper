package groq

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vaporon4a/movie-helper/internal/ai"
	"github.com/vaporon4a/movie-helper/internal/daily"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type budget struct {
	allowed bool
	calls   int
}

func (b *budget) AllowAPI(context.Context, string, int) (bool, error) {
	b.calls++
	return b.allowed, nil
}
func response(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
func answer(s, finish string) string {
	b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"finish_reason": finish, "message": map[string]string{"content": s}}}})
	return string(b)
}
func client(rt transport, b *budget) *Client {
	return &Client{HTTP: &http.Client{Transport: rt}, BaseURL: "https://groq.invalid/openai/v1", Key: "test-secret", Model: "qwen/qwen3.8-27b", Budget: b, DailyLimit: 6, Now: time.Now}
}

func TestVisionPayloadSelectionAndBudget(t *testing.T) {
	png, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aX1sAAAAASUVORK5CYII=")
	b := &budget{allowed: true}
	images, calls := 0, 0
	c := client(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "i.redd.it" {
			images++
			return response(200, string(png)), nil
		}
		calls++
		if r.URL.Path != "/openai/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer test-secret" || r.Header.Get("x-goog-api-key") != "" {
			t.Fatal("invalid auth or endpoint")
		}
		var req struct {
			Model     string
			Reasoning string            `json:"reasoning_effort"`
			Max       int               `json:"max_completion_tokens"`
			Format    map[string]string `json:"response_format"`
			Messages  []struct {
				Role    string
				Content json.RawMessage
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.Model != cModel || req.Max != 512 || req.Reasoning != "none" || req.Format["type"] != "json_object" || len(req.Messages) != 2 {
			t.Fatal("invalid generation options")
		}
		var parts []struct {
			Type  string
			Image map[string]string `json:"image_url"`
		}
		if err := json.Unmarshal(req.Messages[1].Content, &parts); err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, p := range parts {
			if p.Type == "image_url" {
				n++
				if !strings.HasPrefix(p.Image["url"], "data:image/png;base64,") {
					t.Fatal("image not embedded")
				}
			}
		}
		if n != 2 {
			t.Fatalf("sent %d images", n)
		}
		return response(200, answer(`{"index":1,"text":"invented","evidence":""}`, "stop")), nil
	}, b)
	items := []daily.Item{{Image: "https://i.redd.it/a.png", Key: "a"}, {Image: "https://i.redd.it/b.png", Key: "b"}, {Image: "https://i.redd.it/c.png", Key: "c"}}
	item, err := c.SelectMeme(context.Background(), items)
	if err != nil || item == nil || *item != items[1] || images != 2 || calls != 1 || b.calls != 1 {
		t.Fatal(item, err, images, calls, b.calls)
	}
	b.allowed = false
	_, err = c.Generate(context.Background(), "test", nil)
	if !errors.Is(err, ai.ErrDailyLimit) || calls != 1 {
		t.Fatal("limit bypassed", err)
	}
}

const cModel = "qwen/qwen3.8-27b"

func TestFactValidationAndUpstreamFailures(t *testing.T) {
	article := ai.Article{Title: "Film", Text: "The production used miniature models to build the city.", URL: "https://en.wikipedia.org/?oldid=1", Key: "film", Attribution: "CC BY-SA"}
	for _, tc := range []struct {
		body, finish string
		valid        bool
	}{
		{`{"index":0,"text":"Для фильма построили миниатюры.","evidence":"The production used miniature models"}`, "stop", true},
		{`{"index":0,"text":"Факт","evidence":"Invented evidence that is absent"}`, "stop", false},
		{`{"text":"Факт"}`, "stop", false},
		{`{"index":0,"text":"Факт","evidence":"The production used miniature models"}`, "length", false},
	} {
		c := client(func(*http.Request) (*http.Response, error) { return response(200, answer(tc.body, tc.finish)), nil }, &budget{allowed: true})
		item, err := c.Fact(context.Background(), []ai.Article{article})
		if tc.valid {
			if err != nil || item == nil || item.Source != article.URL || !strings.Contains(item.Text, article.Attribution) {
				t.Fatal(item, err)
			}
		} else if err == nil || item != nil {
			t.Fatal("unvalidated fact accepted")
		}
	}
	for _, code := range []int{401, 403, 404, 429, 503} {
		c := client(func(*http.Request) (*http.Response, error) { return response(code, "private upstream body"), nil }, &budget{allowed: true})
		_, err := c.Generate(context.Background(), "test", nil)
		var status *HTTPError
		if !errors.As(err, &status) || status.Status != code || strings.Contains(err.Error(), "private") {
			t.Fatal(err)
		}
	}
}

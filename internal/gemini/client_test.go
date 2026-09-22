package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
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
func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
func answer(s string) string {
	b, _ := json.Marshal(map[string]any{"candidates": []any{map[string]any{"finishReason": "STOP", "content": map[string]any{"parts": []any{map[string]any{"text": s}}}}}})
	return string(b)
}
func testClient(rt transport, b *budget) *Client {
	return &Client{HTTP: &http.Client{Transport: rt}, BaseURL: "https://gemini.invalid/v1beta", Key: "secret", Model: "gemini-3.6-flash", Budget: b, DailyLimit: 6, Now: time.Now}
}

func TestFactProvenanceAndBudget(t *testing.T) {
	b := &budget{allowed: true}
	calls := 0
	c := testClient(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("x-goog-api-key") != "secret" || strings.Contains(r.URL.String(), "secret") {
			t.Error("key transport")
		}
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if _, ok := req["tools"]; ok {
			t.Error("Google Search unexpectedly enabled")
		}
		if req["systemInstruction"] == nil || req["generationConfig"] == nil {
			t.Error("missing constraints")
		}
		return response(200, answer(`{"index":0,"text":"Для фильма использовали миниатюры.","evidence":"The production used miniature models"}`)), nil
	}, b)
	a := Article{Title: "Film", Text: "The production used miniature models to build the city.", URL: "https://en.wikipedia.org/w/index.php?oldid=123", Key: "wiki:Film", Attribution: "Wikipedia CC BY-SA 4.0"}
	got, err := c.Fact(context.Background(), []Article{a})
	if err != nil || got == nil || got.Source != a.URL || got.Key != a.Key || !strings.Contains(got.Text, a.Attribution) {
		t.Fatal(got, err)
	}
	b.allowed = false
	if _, err = c.Fact(context.Background(), []Article{a}); err == nil || calls != 1 {
		t.Fatal("cap not enforced", err, calls)
	}
}
func TestFactRejectsInventedEvidenceAndMalformedOutput(t *testing.T) {
	for _, s := range []string{`{"index":0,"text":"Факт","evidence":"Invented text not present in source"}`, `{"index":4,"text":"Факт","evidence":"The production used miniature models"}`, `{"text":"Факт"}`, `{"index":0,"text":"","evidence":"The production used miniature models"}`, "not JSON"} {
		c := testClient(func(*http.Request) (*http.Response, error) { return response(200, answer(s)), nil }, &budget{allowed: true})
		if i, err := c.Fact(context.Background(), []Article{{Text: "The production used miniature models"}}); err == nil || i != nil {
			t.Fatalf("accepted %s", s)
		}
	}
	c := testClient(func(*http.Request) (*http.Response, error) { return response(429, "secret upstream body"), nil }, &budget{allowed: true})
	if _, err := c.Fact(context.Background(), []Article{{Text: "text"}}); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatal(err)
	}
}
func TestHTTPFailuresAndLocalBudgetAreDistinct(t *testing.T) {
	for _, code := range []int{401, 403, 404, 429, 503} {
		c := testClient(func(*http.Request) (*http.Response, error) { return response(code, "secret upstream body"), nil }, &budget{allowed: true})
		_, err := c.Generate(context.Background(), "test", nil)
		var httpErr *HTTPError
		if !errors.As(err, &httpErr) || httpErr.Status != code || strings.Contains(err.Error(), "secret") {
			t.Fatal(code, err)
		}
	}
	c := testClient(func(*http.Request) (*http.Response, error) {
		t.Fatal("request after budget exhausted")
		return nil, nil
	}, &budget{})
	_, err := c.Generate(context.Background(), "test", nil)
	if !errors.Is(err, ErrDailyLimit) {
		t.Fatal(err)
	}
}

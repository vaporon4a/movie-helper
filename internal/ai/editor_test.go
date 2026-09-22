package ai

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/vaporon4a/movie-helper/internal/daily"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type generatorFunc func(context.Context, string, []Part) (Selection, error)

func (f generatorFunc) Generate(ctx context.Context, s string, p []Part) (Selection, error) {
	return f(ctx, s, p)
}
func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
func TestImageRestrictionsAndActualCandidateSelection(t *testing.T) {
	png, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aX1sAAAAASUVORK5CYII=")
	images := 0
	c := &Editor{MaxImages: 4, Generator: generatorFunc(func(context.Context, string, []Part) (Selection, error) {
		n := 0
		return Selection{Index: &n, Text: "invented caption"}, nil
	}), HTTP: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "i.redd.it" {
			images++
			return response(200, string(png)), nil
		}
		t.Fatal("unexpected HTTP request")
		return nil, nil
	})}}
	for _, raw := range []string{"http://i.redd.it/x.png", "https://evil.test/x.png", "https://i.redd.it:443/x.png", "https://x@i.redd.it/x.png"} {
		if _, err := c.image(context.Background(), raw); err == nil {
			t.Fatal("accepted", raw)
		}
	}
	items := []daily.Item{{Key: "a", Image: "https://i.redd.it/a.png", Text: "Original"}}
	got, err := c.SelectMeme(context.Background(), items)
	if err != nil || got == nil || *got != items[0] || images != 1 {
		t.Fatal(got, err, images)
	}
	c.HTTP.Transport = transport(func(*http.Request) (*http.Response, error) {
		r := response(302, "")
		r.Header.Set("Location", "https://evil.test/a.png")
		return r, nil
	})
	if _, err := c.image(context.Background(), items[0].Image); err == nil {
		t.Fatal("redirect accepted")
	}
	c.HTTP.Transport = transport(func(*http.Request) (*http.Response, error) { return response(200, strings.Repeat("x", (2<<20)+1)), nil })
	if _, err := c.image(context.Background(), items[0].Image); err == nil {
		t.Fatal("large image accepted")
	}
}

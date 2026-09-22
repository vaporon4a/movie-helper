package ai

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"net/http"
	"testing"
	"time"

	"github.com/vaporon4a/movie-helper/internal/daily"
)

type reviewCache map[string]bool

func (r reviewCache) MemeRejected(_ context.Context, scope, key string, _ time.Time) (bool, error) {
	return r[scope+key], nil
}
func (r reviewCache) RejectMeme(_ context.Context, scope, key string, _ time.Time) error {
	r[scope+key] = true
	return nil
}
func index(n int) *int { return &n }
func decision(reason string) Selection {
	n := -1
	if reason == "accepted" {
		n = 0
	}
	return Selection{Index: &n, Reviews: []Review{{Index: index(0), Reason: reason, Detail: "Краткая причина"}}}
}

func TestMemeBatchesCacheAndFailures(t *testing.T) {
	var fixture bytes.Buffer
	if err := png.Encode(&fixture, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	items := []daily.Item{{Key: "a", Image: "https://i.redd.it/a.png"}, {Key: "b", Image: "https://i.redd.it/b.png"}, {Key: "c", Image: "https://i.redd.it/c.png"}}
	cache := reviewCache{}
	var fetched []string
	c := &Editor{MaxImages: 1, MaxBatches: 2, Scope: "groq:model:v2", Reviews: cache, HTTP: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		fetched = append(fetched, r.URL.Path)
		return response(200, fixture.String()), nil
	})}}
	calls := 0
	c.Generator = generatorFunc(func(context.Context, string, []Part) (Selection, error) { calls++; return decision("not_meme"), nil })
	got, err := c.SelectMeme(context.Background(), items)
	if err != nil || got != nil || calls != 2 || len(cache) != 2 {
		t.Fatal(got, err, calls, cache)
	}
	c.Generator = generatorFunc(func(context.Context, string, []Part) (Selection, error) { calls++; return decision("accepted"), nil })
	got, err = c.SelectMeme(context.Background(), items)
	if err != nil || got == nil || *got != items[2] || calls != 3 || len(fetched) != 3 || fetched[2] != "/c.png" {
		t.Fatal(got, err, calls, fetched)
	}
	// Provider failures must not poison the rejection cache or retry a batch.
	c.Generator = generatorFunc(func(context.Context, string, []Part) (Selection, error) {
		calls++
		return Selection{}, errors.New("offline")
	})
	got, err = c.SelectMeme(context.Background(), items)
	if err == nil || got != nil || calls != 4 || len(cache) != 2 {
		t.Fatal(got, err, calls, cache)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = c.SelectMeme(ctx, items)
	if !errors.Is(err, context.Canceled) || calls != 4 {
		t.Fatal(err, calls)
	}
}

func TestMemeReviewValidationFailsClosed(t *testing.T) {
	for _, s := range []Selection{
		{Index: index(0)},
		{Index: index(0), Reviews: decision("unsuitable").Reviews},
		{Index: index(-1), Reviews: decision("accepted").Reviews},
		{Index: index(0), Reviews: []Review{{Index: index(1), Reason: "accepted", Detail: "ok"}}},
		{Index: index(-1), Reviews: []Review{{Index: index(0), Reason: "invented", Detail: "ok"}}},
		{Index: index(-1), Reviews: []Review{{Index: nil, Reason: "not_meme", Detail: "ok"}}},
	} {
		if validateReviews(s, 1) == nil {
			t.Fatal("invalid review accepted", s)
		}
	}
	s := Selection{Index: index(-1), Reviews: []Review{{Index: index(0), Reason: "not_meme", Detail: "ok"}, {Index: index(0), Reason: "not_meme", Detail: "ok"}}}
	if validateReviews(s, 2) == nil {
		t.Fatal("duplicate index accepted")
	}
}

func TestAnalysisImageResizesAndRejectsCorruptData(t *testing.T) {
	var raw bytes.Buffer
	if err := jpeg.Encode(&raw, image.NewRGBA(image.Rect(0, 0, 2560, 1280)), &jpeg.Options{Quality: 100}); err != nil {
		t.Fatal(err)
	}
	data, mime, w, h, err := analysisImage(raw.Bytes())
	if err != nil || mime != "image/jpeg" || w != 1280 || h != 640 || len(data) >= raw.Len() {
		t.Fatal(mime, w, h, err, len(data), raw.Len())
	}
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil || decoded.Bounds().Dx() != 1280 || decoded.Bounds().Dy() != 640 {
		t.Fatal(err)
	}
	if _, _, _, _, err = analysisImage(raw.Bytes()[:100]); err == nil {
		t.Fatal("corrupt image accepted")
	}
	// Highly compressible files must still satisfy the decoded pixel bound.
	raw.Reset()
	if err := png.Encode(&raw, image.NewGray(image.Rect(0, 0, 5000, 4000))); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err = analysisImage(raw.Bytes()); err == nil {
		t.Fatal("oversized decoded image accepted")
	}
}

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

func decision(reason string) Selection {
	n := -1
	if reason == "accepted" {
		n = 0
	}
	return Selection{Index: &n, Reviews: []Review{{Index: new(0), Reason: reason, Detail: "Краткая причина"}}}
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
		{Index: new(0)},
		{Index: new(0), Reviews: decision("unsuitable").Reviews},
		{Index: new(-1), Reviews: decision("accepted").Reviews},
		{Index: new(0), Reviews: []Review{{Index: new(1), Reason: "accepted", Detail: "ok"}}},
		{Index: new(-1), Reviews: []Review{{Index: new(0), Reason: "invented", Detail: "ok"}}},
		{Index: new(-1), Reviews: []Review{{Index: nil, Reason: "not_meme", Detail: "ok"}}},
	} {
		if validateReviews(s, 1) == nil {
			t.Fatal("invalid review accepted", s)
		}
	}
	s := Selection{Index: new(-1), Reviews: []Review{{Index: new(0), Reason: "not_meme", Detail: "ok"}, {Index: new(0), Reason: "not_meme", Detail: "ok"}}}
	if validateReviews(s, 2) == nil {
		t.Fatal("duplicate index accepted")
	}
}

func TestSelectMemesReturnsChosenFirstAndKeepsOtherAccepted(t *testing.T) {
	var fixture bytes.Buffer
	if err := png.Encode(&fixture, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	items := []daily.Item{
		{Key: "a", Image: "https://i.redd.it/a.png"},
		{Key: "b", Image: "https://i.redd.it/b.png"},
		{Key: "c", Image: "https://i.redd.it/c.png"},
	}
	cache := reviewCache{}
	c := &Editor{
		MaxImages: 3, MaxBatches: 1, Scope: "gemini:model:" + MemeReviewVersion, Reviews: cache,
		HTTP: &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) { return response(200, fixture.String()), nil })},
		Generator: generatorFunc(func(context.Context, string, []Part) (Selection, error) {
			selected := 1
			return Selection{Index: &selected, Reviews: []Review{
				{Index: new(0), Reason: "accepted", Detail: "подходит"},
				{Index: new(1), Reason: "accepted", Detail: "лучший"},
				{Index: new(2), Reason: "low_humor", Detail: "слабая шутка"},
			}}, nil
		}),
	}
	got, err := c.SelectMemes(context.Background(), items, 3)
	if err != nil || len(got) != 2 || got[0].Key != "b" || got[1].Key != "a" {
		t.Fatal(got, err)
	}
	if !cache[c.Scope+"c"] || cache[c.Scope+"a"] || cache[c.Scope+"b"] {
		t.Fatal(cache)
	}
}

func TestTechnicalImageFailureIsSharedAndCached(t *testing.T) {
	cache := reviewCache{}
	fetches := 0
	c := &Editor{
		MaxImages: 1, MaxBatches: 1, Scope: "gemini:model:" + MemeReviewVersion, Reviews: cache,
		HTTP: &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
			fetches++
			return response(200, string(make([]byte, maxSourceImageBytes+1))), nil
		})},
		Generator: generatorFunc(func(context.Context, string, []Part) (Selection, error) {
			t.Fatal("generator called for invalid image")
			return Selection{}, nil
		}),
	}
	item := daily.Item{Key: "too-large", Image: "https://i.redd.it/large.png"}
	for range 2 {
		got, err := c.SelectMemes(context.Background(), []daily.Item{item}, 1)
		if err != nil || len(got) != 0 {
			t.Fatal(got, err)
		}
	}
	if fetches != 1 || !cache[memeImageScope+item.Key] {
		t.Fatal(fetches, cache)
	}
}

func TestPreviewFallsBackToOriginalAndTransientFailureIsRetried(t *testing.T) {
	var fixture bytes.Buffer
	if err := png.Encode(&fixture, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	cache := reviewCache{}
	fetches := 0
	c := &Editor{
		MaxImages: 1, MaxBatches: 1, Scope: "gemini:model:" + MemeReviewVersion, Reviews: cache,
		HTTP: &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
			fetches++
			if r.URL.Host == "preview.redd.it" {
				return response(http.StatusNotFound, ""), nil
			}
			return response(http.StatusOK, fixture.String()), nil
		})},
		Generator: generatorFunc(func(context.Context, string, []Part) (Selection, error) { return decision("accepted"), nil }),
	}
	item := daily.Item{Key: "fallback", Image: "https://i.redd.it/fallback.png", AnalysisImage: "https://preview.redd.it/fallback.png?width=1080&s=signature"}
	got, err := c.SelectMemes(context.Background(), []daily.Item{item}, 1)
	if err != nil || len(got) != 1 || fetches != 2 || cache[memeImageScope+item.Key] {
		t.Fatal(got, err, fetches, cache)
	}

	cache = reviewCache{}
	fetches = 0
	c.Reviews = cache
	c.HTTP.Transport = transport(func(*http.Request) (*http.Response, error) {
		fetches++
		return nil, errors.New("temporary network failure")
	})
	item.AnalysisImage = ""
	for range 2 {
		got, err = c.SelectMemes(context.Background(), []daily.Item{item}, 1)
		if err != nil || len(got) != 0 {
			t.Fatal(got, err)
		}
	}
	if fetches != 2 || cache[memeImageScope+item.Key] {
		t.Fatal(fetches, cache)
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

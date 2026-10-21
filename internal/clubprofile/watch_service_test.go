package clubprofile_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/vaporon4a/movie-helper/internal/clubprofile"
)

type filmCatalog struct {
	fail  bool
	calls int
}

func (f *filmCatalog) SearchFilms(context.Context, string) ([]clubprofile.Film, error) {
	f.calls++
	if f.fail {
		return nil, errors.New("offline")
	}
	return []clubprofile.Film{{TMDBID: 603, Title: "Матрица", Source: "tmdb"}, {TMDBID: 604, Title: "Матрица 2", Source: "tmdb"}}, nil
}
func (f *filmCatalog) FilmIdentity(_ context.Context, id int64) (clubprofile.Film, error) {
	f.calls++
	if f.fail {
		return clubprofile.Film{}, errors.New("offline")
	}
	return clubprofile.Film{TMDBID: id, Title: "Матрица", Source: "tmdb", FetchedAt: time.Now()}, nil
}
func (f *filmCatalog) FilmDetails(ctx context.Context, id int64) (clubprofile.Film, error) {
	film, err := f.FilmIdentity(ctx, id)
	film.Metadata.Complete = true
	return film, err
}

type cardEditor struct{ edits int }

func (e *cardEditor) PublishWatchCard(context.Context, clubprofile.WatchDraft) error {
	e.edits++
	return nil
}

func TestWatchDraftRetryExpiryAndChatScopedSelection(t *testing.T) {
	store, _, _, _, _, now := profileApp(t)
	ctx := t.Context()
	catalog := &filmCatalog{fail: true}
	editor := &cardEditor{}
	s, err := clubprofile.NewWatchService(store, catalog, map[int64]bool{-1: true, -2: true}, slog.New(slog.DiscardHandler), func() time.Time { return *now })
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.Add(ctx, 300, -1, "https://www.themoviedb.org/movie/603")
	if err != nil || d.State != "pending" {
		t.Fatal(d, err)
	}
	ok, err := s.ClaimMessage(ctx, -1, d.ID)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if ok, err = s.ClaimMessage(ctx, -1, d.ID); err != nil || ok {
		t.Fatal("duplicate card reservation")
	}
	if err = s.BindMessage(ctx, -1, d.ID, 42); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(6 * time.Minute)
	catalog.fail = false
	if err = s.Tick(ctx, editor); err != nil {
		t.Fatal(err)
	}
	d, err = store.WatchDraft(ctx, -1, d.ID)
	if err != nil || d.State != "saved" || d.WatchID == 0 || editor.edits != 1 {
		t.Fatal(d, editor, err)
	}
	if err = s.Tick(ctx, editor); err != nil || editor.edits != 1 {
		t.Fatal("duplicate edit", err)
	}
	d, err = s.Add(ctx, 301, -1, "Матрица")
	if err != nil || d.State != "choice" {
		t.Fatal(d, err)
	}
	if _, err = s.Choose(ctx, -2, d.ID, 603); err == nil {
		t.Fatal("cross-chat choice")
	}
	if _, err = s.Choose(ctx, -1, d.ID, 999); err == nil {
		t.Fatal("unoffered choice")
	}
	choice, err := s.Choose(ctx, -1, d.ID, 604)
	if err != nil || choice.State != "saved" {
		t.Fatal(choice, err)
	}
	if _, err = s.Choose(ctx, -1, d.ID, 603); err != nil {
		t.Fatal("idempotent callback", err)
	}
	ws, _, err := s.History(ctx, -1)
	if err != nil || len(ws) != 2 {
		t.Fatal(ws, err)
	}
	catalog.fail = true
	expired, err := s.Add(ctx, 302, -1, "Другой фильм")
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(25 * time.Hour)
	if err = s.Tick(ctx, editor); err != nil {
		t.Fatal(err)
	}
	expired, err = store.WatchDraft(ctx, -1, expired.ID)
	if err != nil || expired.State != "expired" {
		t.Fatal(expired, err)
	}
}

type deleter struct {
	calls int
	err   error
}

func (d *deleter) DeleteProfilePoll(context.Context, clubprofile.CleanupJob) error {
	d.calls++
	return d.err
}

func TestCleanupOnlyAfterFinalAndPermissionFailureDoesNotLoseAnswers(t *testing.T) {
	store, s, c, _, _, now := profileApp(t)
	ctx := t.Context()
	_, err := s.Start(ctx, clubprofile.Genre, 400, -1, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	d := &deleter{err: &clubprofile.DeliveryError{Kind: "forbidden", Reason: "forbidden"}}
	w := clubprofile.CleanupWorker{Store: store, Transport: d, Allowed: map[int64]bool{-1: true}, Log: slog.New(slog.DiscardHandler), Now: func() time.Time { return *now }}
	if err = w.Tick(ctx); err != nil || d.calls != 0 {
		t.Fatal("deleted unfinished poll", err)
	}
	*now = now.Add(6 * time.Minute)
	if err = c.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if err = w.Tick(ctx); err != nil || d.calls != 1 {
		t.Fatal(err, d.calls)
	}
	if err = w.Tick(ctx); err != nil || d.calls != 1 {
		t.Fatal("permanent failure loop", err)
	}
	polls, err := store.ProfilePolls(ctx, -1, clubprofile.Completed)
	if err != nil || len(polls) != 1 || polls[0].Result == nil {
		t.Fatal("lost final", polls, err)
	}
}

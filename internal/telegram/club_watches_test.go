package telegram

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/vaporon4a/movie-helper/internal/clubprofile"
)

type watchCatalog struct{}

func (watchCatalog) SearchFilms(context.Context, string) ([]clubprofile.Film, error) {
	return []clubprofile.Film{{TMDBID: 603, Title: "Матрица"}}, nil
}
func (watchCatalog) FilmIdentity(context.Context, int64) (clubprofile.Film, error) {
	return clubprofile.Film{TMDBID: 603, Title: "Матрица", Year: 1999, Source: "tmdb"}, nil
}
func (c watchCatalog) FilmDetails(ctx context.Context, id int64) (clubprofile.Film, error) {
	return c.FilmIdentity(ctx, id)
}

func TestWatchedCommandOneCardEqualMembersAndQuietCorrections(t *testing.T) {
	h, api := handler(t)
	ctx := t.Context()
	store := appOf(t, h).store
	var err error
	h.Profile, err = clubprofile.NewService(store, clubprofile.ProfileQuestions{History: store}, h.Allowed, h.Log, h.Now)
	if err != nil {
		t.Fatal(err)
	}
	h.Watches, err = clubprofile.NewWatchService(store, watchCatalog{}, h.Allowed, h.Log, h.Now)
	if err != nil {
		t.Fatal(err)
	}
	h.Handle(ctx, nil, update(500, -1, 42, "/movie_watched https://www.themoviedb.org/movie/603"))
	if len(api.messages) != 1 || !strings.Contains(api.messages[0].Text, "Просмотр сохранён") || !strings.Contains(api.messages[0].Text, "Обсуждение:") || len(api.photos) != 0 {
		t.Fatal(api.messages)
	}
	h.Handle(ctx, nil, update(500, -1, 42, "/movie_watched https://www.themoviedb.org/movie/603"))
	if len(api.messages) != 1 {
		t.Fatal("replayed command sent another card")
	}
	click := func(op int64, data string) {
		h.Handle(ctx, nil, &models.Update{ID: op, CallbackQuery: &models.CallbackQuery{ID: "x", From: models.User{ID: 43}, Data: data, Message: models.MaybeInaccessibleMessage{Message: &models.Message{ID: 99, Chat: models.Chat{ID: -1}}}}})
	}
	for i, data := range []string{"cp:annul:1", "cp:edit:1", "cp:pause:1", "cp:view:watch:1"} {
		click(510+int64(i), data)
		watches, e := store.ProfileWatches(ctx, -1)
		if e != nil || len(watches) != 1 || watches[0].State != "active" || watches[0].Revision != 1 {
			t.Fatalf("button %s changed watch: %+v, %v", data, watches, e)
		}
		sessions, e := store.ProfileSessions(ctx, -1)
		if e != nil || len(sessions) != 1 || sessions[0].State != "queued" {
			t.Fatalf("button %s stopped discussion: %+v, %v", data, sessions, e)
		}
		if len(api.messages) != 1 {
			t.Fatal("button help sent a new group message")
		}
	}
	click(520, "cp:pause:1")
	if !strings.Contains(api.edits[len(api.edits)-1].Text, "Серия сама не возобновится") {
		t.Fatal("pause did not explain consequences")
	}
	click(521, "cp:pause_confirm:1:1")
	click(521, "cp:pause_confirm:1:1")
	watches, err := store.ProfileWatches(ctx, -1)
	if err != nil || len(watches) != 1 || watches[0].State != "active" || watches[0].Revision != 2 {
		t.Fatal("confirmation replay changed watch twice", watches, err)
	}
	sessions, err := store.ProfileSessions(ctx, -1)
	if err != nil || len(sessions) != 1 || sessions[0].State != "cancelled" {
		t.Fatal(sessions, err)
	}
	if !strings.Contains(api.edits[len(api.edits)-1].Text, "остановлено участником") {
		t.Fatal(api.edits)
	}
	h.Handle(ctx, nil, update(502, -1, 43, "/movie_profile"))
	if !strings.Contains(api.messages[len(api.messages)-1].Text, "1 разных фильмов") {
		t.Fatal(api.messages)
	}
}

func TestWatchCardHasNoCancellationAndPauseNeedsExplicitConfirmation(t *testing.T) {
	v := clubProfileView{watches: []clubprofile.WatchEvent{{ID: 2, State: "active", Film: clubprofile.Film{Title: "Она"}}}}
	for _, page := range []string{"watch", "edit", "pause"} {
		text, keyboard := v.page(page, 2)
		confirmation := false
		for _, row := range keyboard.InlineKeyboard {
			for _, b := range row {
				if strings.HasPrefix(b.CallbackData, "cp:annul:") {
					t.Fatal("cancellation button remains", page)
				}
				confirmation = confirmation || strings.HasPrefix(b.CallbackData, "cp:pause_confirm:2:")
			}
		}
		if confirmation != (page == "pause") {
			t.Fatal("confirmation must appear only on explicit pause screen", page)
		}
		if page == "edit" && !strings.Contains(text, "/movie_watch_date 2 YYYY-MM-DD") {
			t.Fatal("missing manual date instruction")
		}
	}
	_, keyboard := v.page("pause", 999)
	for _, row := range keyboard.InlineKeyboard {
		for _, b := range row {
			if strings.HasPrefix(b.CallbackData, "cp:pause_confirm:") {
				t.Fatal("confirmation offered for a missing watch")
			}
		}
	}
}

func TestWatchFirstCardRetriesConfirmed429AfterDeadline(t *testing.T) {
	h, api := handler(t)
	store := appOf(t, h).store
	ctx := t.Context()
	if err := store.EnsureChat(ctx, -1); err != nil {
		t.Fatal(err)
	}
	now := h.Now()
	h.Now = func() time.Time { return now }
	var err error
	h.Profile, err = clubprofile.NewService(store, clubprofile.ProfileQuestions{History: store}, h.Allowed, h.Log, h.Now)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := clubprofile.NewWatchService(store, watchCatalog{}, h.Allowed, h.Log, h.Now)
	if err != nil {
		t.Fatal(err)
	}
	h.Watches = svc
	api.errorSend = &bot.TooManyRequestsError{RetryAfter: 120}
	h.Handle(ctx, nil, update(991, -1, 42, "/movie_watched Матрица"))
	draft, err := store.WatchDraft(ctx, -1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(api.messages) != 1 {
		t.Fatal("retry generated a separate error message")
	}
	api.errorSend = nil
	// Retry is durable and respects Retry-After, even with a fresh service instance.
	svc, err = clubprofile.NewWatchService(store, watchCatalog{}, h.Allowed, h.Log, h.Now)
	if err != nil {
		t.Fatal(err)
	}
	h.Watches = svc
	now = now.Add(time.Minute)
	if err = svc.Tick(ctx, h); err != nil {
		t.Fatal(err)
	}
	if len(api.messages) != 1 {
		t.Fatal("retried too soon")
	}
	now = now.Add(time.Minute)
	for range 3 {
		if err = svc.Tick(ctx, h); err != nil {
			t.Fatal(err)
		}
	}
	draft, err = store.WatchDraft(ctx, -1, draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	if draft.MessageID != 99 || draft.State != "choice" || len(api.messages) != 2 || len(api.edits) != 0 {
		t.Fatalf("card retry failed: %+v sends=%d edits=%d", draft, len(api.messages), len(api.edits))
	}
	if api.messages[1].ReplyMarkup == nil {
		t.Fatal("missing movie choice buttons")
	}
}

func TestWatchFirstCardAmbiguousDeliveryIsNotRetried(t *testing.T) {
	h, api := handler(t)
	store := appOf(t, h).store
	ctx := t.Context()
	if err := store.EnsureChat(ctx, -1); err != nil {
		t.Fatal(err)
	}
	svc, err := clubprofile.NewWatchService(store, watchCatalog{}, h.Allowed, h.Log, h.Now)
	if err != nil {
		t.Fatal(err)
	}
	h.Watches = svc
	draft, err := svc.Add(ctx, 992, -1, "Матрица")
	if err != nil {
		t.Fatal(err)
	}
	api.errorSend = errors.New("connection lost after write")
	if err = h.sendWatchCard(ctx, draft); err == nil {
		t.Fatal("expected ambiguous result")
	}
	api.errorSend = nil
	if err = svc.Tick(ctx, h); err != nil {
		t.Fatal(err)
	}
	draft, err = store.WatchDraft(ctx, -1, draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	if draft.MessageID != -1 || len(api.messages) != 1 {
		t.Fatal("ambiguous card resent")
	}
}

type failedWatchRetry struct {
	WatchApplication
	err error
}

func (w failedWatchRetry) RetryMessage(context.Context, int64, int64, time.Duration) error {
	return w.err
}

func TestWatchCardDoesNotReportRetryWhenPersistenceFails(t *testing.T) {
	h, api := handler(t)
	store := appOf(t, h).store
	ctx := t.Context()
	if err := store.EnsureChat(ctx, -1); err != nil {
		t.Fatal(err)
	}
	svc, err := clubprofile.NewWatchService(store, watchCatalog{}, h.Allowed, h.Log, h.Now)
	if err != nil {
		t.Fatal(err)
	}
	persistErr := errors.New("storage unavailable")
	h.Watches = failedWatchRetry{WatchApplication: svc, err: persistErr}
	api.errorSend = &bot.TooManyRequestsError{RetryAfter: 120}
	err = h.addWatched(ctx, update(993, -1, 42, "/movie_watched Матрица"), "Матрица")
	if !errors.Is(err, persistErr) || errors.Is(err, errResponseSent) || errors.Is(err, errWatchCardRetryScheduled) {
		t.Fatalf("failed persistence was hidden: %v", err)
	}
}

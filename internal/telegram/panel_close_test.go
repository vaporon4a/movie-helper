package telegram

import (
	"errors"
	"strings"
	"testing"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/vaporon4a/movie-helper/internal/clubprofile"
)

func assertCloseFooter(t *testing.T, markup models.ReplyMarkup) {
	t.Helper()
	k, ok := markup.(*models.InlineKeyboardMarkup)
	if !ok || len(k.InlineKeyboard) == 0 {
		t.Fatal("missing panel keyboard")
	}
	last := k.InlineKeyboard[len(k.InlineKeyboard)-1]
	if len(last) != 1 || last[0].CallbackData != closePanelAction {
		t.Fatal("Close must be a separate last row", k)
	}
	count := 0
	for _, row := range k.InlineKeyboard {
		for _, b := range row {
			if b.CallbackData == closePanelAction {
				count++
			}
		}
	}
	if count != 1 {
		t.Fatal("duplicate Close buttons", count)
	}
}

func closeClick(op, chat, user int64, kind models.ChatType) *models.Update {
	u := menuClick(op, chat, user, "home", kind)
	u.CallbackQuery.Data = closePanelAction
	return u
}

func TestClosePanelDeletesMessageAndKeepsViewingAndResearch(t *testing.T) {
	h, api := handler(t)
	attachMenuServices(t, h)
	store := appOf(t, h).store
	var err error
	svc, err := clubprofile.NewWatchService(store, watchCatalog{}, h.Allowed, h.Log, h.Now)
	if err != nil {
		t.Fatal(err)
	}
	h.Watches = svc
	ctx := t.Context()
	h.Handle(ctx, nil, update(1, -1, 7, "/movie_watched https://www.themoviedb.org/movie/603"))
	assertCloseFooter(t, api.messages[len(api.messages)-1].ReplyMarkup)
	h.Handle(ctx, nil, closeClick(2, -1, 8, models.ChatTypeSupergroup))
	if len(api.deletes) != 1 || api.deletes[0].ChatID != int64(-1) || api.deletes[0].MessageID != 99 {
		t.Fatal("wrong panel deleted", api.deletes)
	}
	draft, err := store.WatchDraft(ctx, -1, 1)
	if err != nil || !draft.Closed {
		t.Fatal("watch card closure not persisted", draft, err)
	}
	sessions, err := store.ProfileSessions(ctx, -1)
	if err != nil || len(sessions) != 1 || sessions[0].State != "queued" {
		t.Fatal("closing cancelled research", sessions, err)
	}
	watches, err := store.ProfileWatches(ctx, -1)
	if err != nil || len(watches) != 1 || watches[0].State != "active" || watches[0].Revision != 1 {
		t.Fatal("closing changed viewing", watches, err)
	}
	edits, sends := len(api.edits), len(api.messages)
	// A new service instance must also respect persisted closure.
	svc, err = clubprofile.NewWatchService(store, watchCatalog{}, h.Allowed, h.Log, h.Now)
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.Tick(ctx, h); err != nil {
		t.Fatal(err)
	}
	if err = h.PublishWatchCard(ctx, draft); err != nil {
		t.Fatal(err)
	}
	if len(api.edits) != edits || len(api.messages) != sends {
		t.Fatal("closed card refreshed or reappeared")
	}
	h.Handle(ctx, nil, update(3, -1, 8, "/movie_profile"))
	assertCloseFooter(t, api.messages[len(api.messages)-1].ReplyMarkup)
	if !strings.Contains(api.messages[len(api.messages)-1].Text, "1 разных фильмов") {
		t.Fatal("history disappeared")
	}
}

func TestClosePanelIsScopedAndFallsBackForOldMessages(t *testing.T) {
	h, api := handler(t)
	ctx := t.Context()
	for _, u := range []*models.Update{
		closeClick(1, -3, 7, models.ChatTypeSupergroup),
		closeClick(2, 7, 8, models.ChatTypePrivate),
		closeClick(3, -1, 7, models.ChatTypeChannel),
	} {
		h.Handle(ctx, nil, u)
	}
	if len(api.deletes) != 0 {
		t.Fatal("foreign panel deleted")
	}
	api.deleteErr = errors.Join(bot.ErrorBadRequest, errors.New("message can't be deleted"))
	h.Handle(ctx, nil, closeClick(4, 7, 7, models.ChatTypePrivate))
	if len(api.markups) != 1 || len(api.messages) != 0 {
		t.Fatal("old panel was not closed quietly")
	}
	k, ok := api.markups[0].ReplyMarkup.(*models.InlineKeyboardMarkup)
	if !ok || len(k.InlineKeyboard) != 0 {
		t.Fatal("fallback left buttons", api.markups)
	}
	api.deleteErr = errors.Join(bot.ErrorBadRequest, errors.New("message to delete not found"))
	h.Handle(ctx, nil, closeClick(5, 7, 7, models.ChatTypePrivate))
	if len(api.markups) != 1 {
		t.Fatal("already deleted panel was edited")
	}
}

func TestCloseFooterExistsAcrossMenusAndDoesNotDuplicate(t *testing.T) {
	h, api := handler(t)
	ctx := t.Context()
	h.Handle(ctx, nil, update(1, -1, 7, "/settings"))
	assertCloseFooter(t, api.messages[0].ReplyMarkup)
	for i, page := range []string{"help", "about", "home", "daily", "movies", "club", "ideas", "advanced", "moderation", "diagnostics"} {
		h.Handle(ctx, nil, menuClick(10+int64(i), -1, 7, page, models.ChatTypeSupergroup))
		assertCloseFooter(t, api.edits[len(api.edits)-1].ReplyMarkup)
	}
	k := closableKeyboard(closableKeyboard(menuBack()))
	assertCloseFooter(t, k)
	h.sendOrEditFeatureView(ctx, 7, 0, "Ideas", menuBack())
	assertCloseFooter(t, api.messages[len(api.messages)-1].ReplyMarkup)
}

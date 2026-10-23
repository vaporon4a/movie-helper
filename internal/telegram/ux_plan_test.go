package telegram

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/vaporon4a/movie-helper/internal/clubprofile"
	"github.com/vaporon4a/movie-helper/internal/scheduleui"
)

type sequentialAPI struct {
	*fakeAPI
	next int
}

func (a *sequentialAPI) SendMessage(ctx context.Context, p *bot.SendMessageParams) (*models.Message, error) {
	m, err := a.fakeAPI.SendMessage(ctx, p)
	a.next++
	m.ID = a.next
	return m, err
}
func clickPanel(t *testing.T, h *Handler, message int, data string, actor int64) {
	t.Helper()
	h.Handle(t.Context(), nil, &models.Update{ID: 900, CallbackQuery: &models.CallbackQuery{ID: "callback", Data: data, From: models.User{ID: actor, FirstName: "Участник"}, Message: models.MaybeInaccessibleMessage{Message: &models.Message{ID: message, Chat: models.Chat{ID: -1, Type: models.ChatTypeSupergroup}}}}})
}
func TestScheduleWizardUsesOnePanelAndDoesNotEnableWhenEditingTime(t *testing.T) {
	h, api := handler(t)
	attachDailyMenu(t, h)
	s := appOf(t, h).store
	h.ScheduleUI = scheduleui.New(s, s)
	clickPanel(t, h, 701, "nav:schedule_genre", 42)
	keyboard := menuKeyboard(t, api.edits[len(api.edits)-1].ReplyMarkup)
	day := keyboard.InlineKeyboard[0][0].CallbackData
	clickPanel(t, h, 701, day, 43)
	keyboard = menuKeyboard(t, api.edits[len(api.edits)-1].ReplyMarkup)
	clock := keyboard.InlineKeyboard[0][0].CallbackData
	clickPanel(t, h, 701, clock, 43)
	if len(api.messages) != 1 || !strings.Contains(api.messages[0].Text, "tg://user?id=43") {
		t.Fatal("addressed input missing", api.messages)
	}
	// Another member cannot accidentally answer somebody else's temporary field.
	reply := update(911, -1, 42, "11:23")
	reply.Message.ReplyToMessage = &models.Message{ID: 99}
	h.Handle(t.Context(), nil, reply)
	c, err := s.ReadMoviesConfig(t.Context(), -1)
	if err != nil || len(c.Schedules) != 0 {
		t.Fatal(c, err)
	}
	reply = update(912, -1, 43, "11:23")
	reply.Message.ReplyToMessage = &models.Message{ID: 99}
	h.Handle(t.Context(), nil, reply)
	keyboard = menuKeyboard(t, api.edits[len(api.edits)-1].ReplyMarkup)
	clickPanel(t, h, 701, keyboard.InlineKeyboard[1][0].CallbackData, 42)
	keyboard = menuKeyboard(t, api.edits[len(api.edits)-1].ReplyMarkup)
	clickPanel(t, h, 701, keyboard.InlineKeyboard[1][0].CallbackData, 42)
	keyboard = menuKeyboard(t, api.edits[len(api.edits)-1].ReplyMarkup)
	confirm := keyboard.InlineKeyboard[0][0].CallbackData
	clickPanel(t, h, 701, confirm, 43)
	c, err = s.ReadMoviesConfig(t.Context(), -1)
	if err != nil || len(c.Schedules) != 1 || c.Schedules[0].Clock != "11:23" || c.Schedules[0].Enabled {
		t.Fatal("unexpected save", c, err)
	}
	clickPanel(t, h, 701, confirm, 42)
	after, err := s.ReadMoviesConfig(t.Context(), -1)
	if err != nil || after.Revision != c.Revision {
		t.Fatal("confirm replay", after, err)
	}
	if len(api.messages) != 1 {
		t.Fatal("wizard spam", len(api.messages))
	}
}
func TestWatchWizardExactLinkWaitsForConfirmationAndAnyMemberCanConfirm(t *testing.T) {
	h, fake := handler(t)
	attachDailyMenu(t, h)
	s := appOf(t, h).store
	h.API = &sequentialAPI{fakeAPI: fake, next: 800}
	watches, err := clubprofile.NewWatchService(s, watchCatalog{}, h.Allowed, h.Log, h.Now)
	if err != nil {
		t.Fatal(err)
	}
	h.Watches = watches
	h.WatchDialog = watches
	h.ProfilePanels = s
	h.Profile, err = clubprofile.NewService(s, clubprofile.ProfileQuestions{History: s}, h.Allowed, h.Log, h.Now)
	if err != nil {
		t.Fatal(err)
	}
	h.Handle(t.Context(), nil, update(921, -1, 42, "/movie_watched"))
	if len(fake.messages) != 2 {
		t.Fatal("unexpected service spam", len(fake.messages))
	}
	reply := update(922, -1, 42, "https://www.themoviedb.org/movie/603")
	reply.Message.ReplyToMessage = &models.Message{ID: 802}
	h.Handle(t.Context(), nil, reply)
	list, err := s.ProfileWatches(t.Context(), -1)
	if err != nil || len(list) != 0 {
		t.Fatal("saved before confirm", list, err)
	}
	keyboard := menuKeyboard(t, fake.edits[len(fake.edits)-1].ReplyMarkup)
	confirm := keyboard.InlineKeyboard[0][0].CallbackData
	clickPanel(t, h, 801, confirm, 43)
	list, err = s.ProfileWatches(t.Context(), -1)
	if err != nil || len(list) != 1 {
		t.Fatal("confirmation failed", list, err)
	}
	clickPanel(t, h, 801, confirm, 42)
	list, err = s.ProfileWatches(t.Context(), -1)
	if err != nil || len(list) != 1 {
		t.Fatal("duplicate", list, err)
	}
}
func TestRepeatedSettingsCommandReusesPanelAndUnknownDoesNotFallback(t *testing.T) {
	h, api := handler(t)
	attachDailyMenu(t, h)
	h.Panels = appOf(t, h).store
	h.Handle(t.Context(), nil, update(930, -1, 42, "/settings"))
	h.Handle(t.Context(), nil, update(931, -1, 42, "/settings"))
	if len(api.messages) != 1 || len(api.edits) != 1 {
		t.Fatal("settings spam", len(api.messages), len(api.edits))
	}
	api.errorSend = errors.New("transport timeout")
	h.Handle(t.Context(), nil, update(932, -1, 43, "/settings"))
	count := len(api.messages)
	h.Handle(t.Context(), nil, update(933, -1, 43, "/settings"))
	if len(api.messages) != count {
		t.Fatal("unknown auto retry")
	}
}

func TestOldWatchCardCannotOverwriteNewWizard(t *testing.T) {
	h, api := handler(t)
	attachDailyMenu(t, h)
	s := appOf(t, h).store
	watches, err := clubprofile.NewWatchService(s, watchCatalog{}, h.Allowed, h.Log, h.Now)
	if err != nil {
		t.Fatal(err)
	}
	h.Watches = watches
	h.WatchDialog = watches
	h.ProfilePanels = s
	h.Profile, err = clubprofile.NewService(s, clubprofile.ProfileQuestions{History: s}, h.Allowed, h.Log, h.Now)
	if err != nil {
		t.Fatal(err)
	}
	h.Handle(t.Context(), nil, update(951, -1, 42, "/movie_watched https://www.themoviedb.org/movie/603"))
	cards, err := s.WatchCards(t.Context(), -1, h.Now())
	if err != nil || len(cards) != 1 {
		t.Fatal(cards, err)
	}
	clickPanel(t, h, 99, "wd:start", 42)
	count := len(api.edits)
	if err = h.EditWatchCard(t.Context(), cards[0]); err != nil {
		t.Fatal(err)
	}
	if len(api.edits) != count {
		t.Fatal("background revived old card")
	}
}

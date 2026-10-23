package telegram

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/vaporon4a/movie-helper/internal/clubprofile"
	"github.com/vaporon4a/movie-helper/internal/clubreminder"
)

func attachMenuServices(t *testing.T, h *Handler) {
	t.Helper()
	store := attachFeatureService(t, h)
	var err error
	h.Profile, err = clubprofile.NewService(store, clubprofile.ProfileQuestions{History: store}, h.Allowed, h.Log, h.Now)
	if err != nil {
		t.Fatal(err)
	}
	h.Club = clubreminder.NewService(store, h.Now, h.Log)
}

func menuClick(op, chat, user int64, page string, kind models.ChatType) *models.Update {
	return &models.Update{ID: op, CallbackQuery: &models.CallbackQuery{ID: "menu", From: models.User{ID: user}, Data: "nav:" + page, Message: models.MaybeInaccessibleMessage{Message: &models.Message{ID: 99, Chat: models.Chat{ID: chat, Type: kind}}}}}
}

func TestHelpHasFiveCoreCommandsAndMenu(t *testing.T) {
	h, api := handler(t)
	h.Handle(t.Context(), nil, update(1, -1, 7, "/help"))
	m := api.messages[len(api.messages)-1]
	var commands []string
	for line := range strings.SplitSeq(m.Text, "\n") {
		if strings.HasPrefix(line, "/") {
			commands = append(commands, strings.Fields(line)[0])
		}
	}
	want := []string{"/movie_watched", "/movie_profile", "/idea", "/my_ideas", "/settings"}
	registered := make([]string, 0, len(CoreCommands()))
	for _, c := range CoreCommands() {
		registered = append(registered, "/"+c.Command)
	}
	if !reflect.DeepEqual(commands, registered) {
		t.Fatal("help and Telegram menu disagree", commands, registered)
	}
	if !reflect.DeepEqual(commands, want) {
		t.Fatal(commands)
	}
	keyboard, ok := m.ReplyMarkup.(*models.InlineKeyboardMarkup)
	if !ok || keyboard.InlineKeyboard[0][0].CallbackData != "nav:home" || keyboard.InlineKeyboard[0][1].CallbackData != "nav:about" {
		t.Fatal(m.ReplyMarkup)
	}
}

func TestMenuNavigationEditsOneMessageAndPreservesSettings(t *testing.T) {
	h, api := handler(t)
	attachMenuServices(t, h)
	ctx := t.Context()
	for i, cmd := range []string{"/timezone Asia/Novosibirsk", "/schedule fact 12:13", "/movie_schedule genre mon 09:00", "/club_schedule fri 2 19:00", "/idea_schedule sun 18:00"} {
		h.Handle(ctx, nil, update(10+int64(i), -1, 7, cmd))
	}
	h.Handle(ctx, nil, update(20, -2, 8, "/timezone Europe/Moscow"))
	before, err := h.Daily.Schedules(ctx, -1)
	if err != nil {
		t.Fatal(err)
	}
	profiles, _, err := h.Profile.Settings(ctx, -1)
	if err != nil {
		t.Fatal(err)
	}
	club, err := h.Club.Settings(ctx, -1)
	if err != nil {
		t.Fatal(err)
	}
	ideas, err := h.Features.Settings(ctx, -1)
	if err != nil {
		t.Fatal(err)
	}
	messages := len(api.messages)
	for i, page := range []string{"home", "timezone", "daily", "movies", "club", "ideas", "backlog", "advanced", "moderation", "diagnostics", "movie_state", "about", "help", "home"} {
		h.Handle(ctx, nil, menuClick(30+int64(i), -1, 8, page, models.ChatTypeSupergroup))
		if len(api.messages) != messages || len(api.edits) != i+1 {
			t.Fatalf("page %s created a message or failed to edit", page)
		}
		edit := api.edits[len(api.edits)-1]
		if edit.ChatID != int64(-1) || edit.MessageID != 99 || edit.Text == "" {
			t.Fatal(edit)
		}
		keyboard, ok := edit.ReplyMarkup.(*models.InlineKeyboardMarkup)
		if !ok {
			t.Fatal("missing navigation", page)
		}
		for _, row := range keyboard.InlineKeyboard {
			for _, b := range row {
				if !knownMenuPage(strings.TrimPrefix(b.CallbackData, "nav:")) {
					t.Fatal("broken menu route", b.CallbackData)
				}
			}
		}
	}
	after, _ := h.Daily.Schedules(ctx, -1)
	afterProfiles, _, _ := h.Profile.Settings(ctx, -1)
	afterClub, _ := h.Club.Settings(ctx, -1)
	afterIdeas, _ := h.Features.Settings(ctx, -1)
	if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(profiles, afterProfiles) || !reflect.DeepEqual(club, afterClub) || !reflect.DeepEqual(ideas, afterIdeas) {
		t.Fatal("navigation changed shared settings")
	}
	h.Handle(ctx, nil, menuClick(60, -2, 7, "home", models.ChatTypeSupergroup))
	text := api.edits[len(api.edits)-1].Text
	if !strings.Contains(text, "Europe/Moscow") || strings.Contains(text, "Asia/Novosibirsk") {
		t.Fatal("menu leaked another chat", text)
	}
}

func TestMenuRejectsForeignChatsAndPrivateDataCallbacks(t *testing.T) {
	h, api := handler(t)
	ctx := t.Context()
	for i, u := range []*models.Update{
		menuClick(1, -3, 7, "home", models.ChatTypeSupergroup),
		menuClick(2, 7, 7, "daily", models.ChatTypePrivate),
		menuClick(3, 7, 8, "about", models.ChatTypePrivate),
		menuClick(4, -1, 7, "unknown", models.ChatTypeSupergroup),
		menuClick(5, -1, 7, "home", models.ChatTypeChannel),
	} {
		h.Handle(ctx, nil, u)
		if len(api.messages) != 0 || len(api.edits) != 0 {
			t.Fatal("unauthorized menu route", i)
		}
	}
	h.Handle(ctx, nil, menuClick(6, 7, 7, "home", models.ChatTypePrivate))
	if len(api.edits) != 1 || !strings.Contains(api.edits[0].Text, "в подключённой группе") {
		t.Fatal("missing private guidance", api.edits)
	}
	h.Handle(ctx, nil, menuClick(7, 7, 7, "about", models.ChatTypePrivate))
	if len(api.edits) != 2 || api.edits[1].Text != about {
		t.Fatal("public about unavailable")
	}
}

func TestMenuRepeatedClickAndMissingServices(t *testing.T) {
	h, api := handler(t)
	ctx := t.Context()
	h.Handle(ctx, nil, update(1, -1, 7, "/settings"))
	for i, page := range []string{"movies", "club", "ideas", "backlog", "movie_state"} {
		h.Handle(ctx, nil, menuClick(10+int64(i), -1, 7, page, models.ChatTypeSupergroup))
		if !strings.Contains(api.edits[len(api.edits)-1].Text, "недоступ") {
			t.Fatal("missing service was not explained", page)
		}
	}
	count := len(api.messages)
	api.errorSend = errors.Join(bot.ErrorBadRequest, errors.New("Bad Request: message is not modified"))
	h.Handle(ctx, nil, menuClick(30, -1, 7, "home", models.ChatTypeSupergroup))
	if len(api.messages) != count {
		t.Fatal("unchanged message generated an error reply")
	}
}

func TestSettingsAliasesKeepNavigation(t *testing.T) {
	h, api := handler(t)
	attachMenuServices(t, h)
	for i, cmd := range []string{"/movie_settings", "/club_settings", "/idea_settings", "/help_manage", "/help_admin"} {
		h.Handle(t.Context(), nil, update(100+int64(i), -1, 8, cmd))
		m := api.messages[len(api.messages)-1]
		if _, ok := m.ReplyMarkup.(*models.InlineKeyboardMarkup); !ok {
			t.Fatal("alias has no navigation", cmd)
		}
	}
}

func TestLongMenuFitsTelegramLimitIncludingEmoji(t *testing.T) {
	text := limitMenuText(strings.Repeat("🎬", 3000))
	if len(utf16.Encode([]rune(text))) > 4096 || strings.ContainsRune(text, '�') {
		t.Fatal("invalid truncated message")
	}
}

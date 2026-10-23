package telegram

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"
	"github.com/vaporon4a/movie-helper/internal/daily"
	"github.com/vaporon4a/movie-helper/internal/interaction"
)

func attachDailyMenu(t *testing.T, h *Handler) {
	t.Helper()
	s := appOf(t, h).store
	h.DailyMenu = daily.NewMenuService(s, s, h.Log)
	h.Interactions = interaction.New(s)
	if err := s.EnsureChat(t.Context(), -1); err != nil {
		t.Fatal(err)
	}
	if err := s.SetZone(t.Context(), 901, -1, "UTC", h.Now()); err != nil {
		t.Fatal(err)
	}
}
func clickDaily(t *testing.T, h *Handler, token string, actor int64) {
	t.Helper()
	h.Handle(t.Context(), nil, &models.Update{ID: 600, CallbackQuery: &models.CallbackQuery{ID: "daily", Data: "dm:" + token, From: models.User{ID: actor, FirstName: "Имя"}, Message: models.MaybeInaccessibleMessage{Message: &models.Message{ID: 777, Chat: models.Chat{ID: -1, Type: models.ChatTypeGroup}}}}})
}
func TestDailyMenuAddressedInputAndClose(t *testing.T) {
	h, api := handler(t)
	attachDailyMenu(t, h)
	if err := h.dailyKindPanel(t.Context(), -1, 777, daily.Fact, "", false); err != nil {
		t.Fatal(err)
	}
	token := strings.TrimPrefix(menuKeyboard(t, api.edits[0].ReplyMarkup).InlineKeyboard[1][0].CallbackData, "dm:")
	clickDaily(t, h, token, 42)
	if len(api.messages) != 1 || !strings.Contains(api.messages[0].Text, "tg://user?id=42") {
		t.Fatal("not addressed")
	}
	reply := update(800, -1, 43, "14:23")
	reply.Message.ReplyToMessage = &models.Message{ID: 99}
	h.Handle(t.Context(), nil, reply)
	c, err := h.DailyMenu.Config(t.Context(), -1)
	if err != nil || c.Schedules[0].Clock == "14:23" {
		t.Fatal("foreign input applied")
	}
	reply.Message.From.ID = 42
	h.Handle(t.Context(), nil, reply)
	c, err = h.DailyMenu.Config(t.Context(), -1)
	if err != nil || c.Schedules[0].Clock != "14:23" || c.Schedules[0].Enabled {
		t.Fatal(c, err)
	}
	if len(api.messages) != 1 || len(api.deletes) == 0 {
		t.Fatal("service message spam", len(api.messages))
	}
}
func TestDailyMenuEmptyStockDoesNotCallForeground(t *testing.T) {
	h, api := handler(t)
	attachDailyMenu(t, h)
	for _, kind := range []string{daily.Meme, daily.Fact} {
		if err := h.dailyKindPanel(t.Context(), -1, 777, kind, "", false); err != nil {
			t.Fatal(err)
		}
		token := strings.TrimPrefix(menuKeyboard(t, api.edits[len(api.edits)-1].ReplyMarkup).InlineKeyboard[0][0].CallbackData, "dm:")
		clickDaily(t, h, token, 42)
		if !strings.Contains(api.edits[len(api.edits)-1].Text, "Фоновая подготовка") {
			t.Fatal("no pending feedback")
		}
	}
	if len(api.photos) != 0 || len(api.messages) != 0 {
		t.Fatal("empty stock sent preview")
	}
}
func TestDailyMenuOldRevisionDoesNotApply(t *testing.T) {
	h, api := handler(t)
	attachDailyMenu(t, h)
	if err := h.dailyKindPanel(t.Context(), -1, 777, daily.Fact, "", false); err != nil {
		t.Fatal(err)
	}
	token := strings.TrimPrefix(menuKeyboard(t, api.edits[0].ReplyMarkup).InlineKeyboard[2][0].CallbackData, "dm:")
	if err := h.Daily.SetZone(t.Context(), 999, -1, "Europe/Moscow", time.Now()); err != nil {
		t.Fatal(err)
	}
	clickDaily(t, h, token, 84)
	c, err := h.DailyMenu.Config(t.Context(), -1)
	if err != nil || c.Schedules[0].Enabled {
		t.Fatal(c, err)
	}
	if !strings.Contains(api.edits[len(api.edits)-1].Text, "устарело") {
		t.Fatal("no stale feedback")
	}
}

func menuKeyboard(t *testing.T, markup models.ReplyMarkup) *models.InlineKeyboardMarkup {
	t.Helper()
	k, ok := markup.(*models.InlineKeyboardMarkup)
	if !ok {
		t.Fatal("missing keyboard")
	}
	return k
}

func TestDailyMenuReadyPreviewCooldownAndUnknown(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(map[bool]string{false: "confirmed", true: "unknown"}[unknown], func(t *testing.T) {
			h, api := handler(t)
			attachDailyMenu(t, h)
			s := appOf(t, h).store
			item := daily.Item{Kind: daily.Fact, Text: "Проверенный факт", Source: "https://example.org/fact", Key: "preview-test", GenerationPolicy: daily.FactPolicy}
			if _, err := s.SaveAIItems(t.Context(), -1, daily.Fact, []daily.Item{item}, h.Now()); err != nil {
				t.Fatal(err)
			}
			show := func() {
				if err := h.dailyKindPanel(t.Context(), -1, 777, daily.Fact, "", false); err != nil {
					t.Fatal(err)
				}
			}
			click := func() {
				k := menuKeyboard(t, api.edits[len(api.edits)-1].ReplyMarkup)
				clickDaily(t, h, strings.TrimPrefix(k.InlineKeyboard[0][0].CallbackData, "dm:"), 42)
			}
			show()
			if unknown {
				api.errorSend = context.DeadlineExceeded
			}
			click()
			api.errorSend = nil
			if len(api.messages) != 1 {
				t.Fatal("no preview", len(api.messages))
			}
			if len(menuKeyboard(t, api.messages[0].ReplyMarkup).InlineKeyboard) != 1 {
				t.Fatal("preview not closable")
			}
			show()
			click()
			if len(api.messages) != 1 {
				t.Fatal("double preview")
			}
			stocks, err := s.AIStocks(t.Context(), -1, h.Now())
			if err != nil {
				t.Fatal(err)
			}
			for _, stock := range stocks {
				if stock.Kind == daily.Fact && stock.Count != 1 {
					t.Fatal("preview consumed daily stock", stocks)
				}
			}
		})
	}
}

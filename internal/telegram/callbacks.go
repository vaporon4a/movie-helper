package telegram

import (
	"context"
	"strconv"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

func (h *Handler) callback(ctx context.Context, update *models.Update) {
	query := update.CallbackQuery
	if query.Message.Message == nil {
		return
	}
	message := query.Message.Message
	if message == nil || query.From.IsBot {
		return
	}
	if h.panelCallback(ctx, query, message) {
		return
	}
	if !h.Allowed[message.Chat.ID] {
		return
	}
	_, _ = h.API.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: query.ID})
	if h.profileCallback(ctx, update.ID, query.Data, message) {
		return
	}
	if retiredMovieCallback(query.Data) {
		h.reply(ctx, message.Chat.ID, "Подборки отключены; фильмы теперь выбираем в чате. История голосований сохранена: /movie_profile")
		return
	}
	action, raw, ok := strings.Cut(query.Data, ":")
	if !ok || (action != "approve" && action != "reject") {
		return
	}
	itemID, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return
	}
	err = h.Daily.Moderate(ctx, update.ID, message.Chat.ID, itemID, query.From.ID, action == "approve", h.Now())
	if err != nil {
		h.reply(ctx, message.Chat.ID, "Материал уже обработан или недоступен в этом чате.")
		return
	}
	h.reply(ctx, message.Chat.ID, "Решение сохранено.")
}

func retiredMovieCallback(data string) bool {
	return strings.HasPrefix(data, "movie_more:") || strings.HasPrefix(data, "movie_taste_reset:")
}

func (h *Handler) panelCallback(ctx context.Context, q *models.CallbackQuery, m *models.Message) bool {
	if h.Interactions != nil && h.Allowed[m.Chat.ID] && q.Data != closePanelAction && !strings.HasPrefix(q.Data, "nav:") {
		screen, err := h.Interactions.PanelScreen(ctx, m.Chat.ID, m.ID)
		if err != nil {
			return true
		}
		want := ""
		switch {
		case strings.HasPrefix(q.Data, "sc:"):
			want = "schedule"
		case strings.HasPrefix(q.Data, "wd:") && q.Data != "wd:start":
			want = watchPage
		case strings.HasPrefix(q.Data, "dm:"):
			want = dailyPage
		case strings.HasPrefix(q.Data, "rc:"):
			want = recoveryPage
		}
		if family, _, ok := strings.Cut(screen, ":"); ok {
			screen = family
		}
		closed, e := h.Interactions.Closed(ctx, m.Chat.ID, m.ID)
		if e != nil || closed || (want != "" && screen != "" && screen != want) {
			return true
		}
	}

	switch {
	case q.Data == closePanelAction:
		h.closePanelCallback(ctx, q, m)
	case strings.HasPrefix(q.Data, "fv:"):
		return h.featureCallback(ctx, q, m)
	case strings.HasPrefix(q.Data, "wd:"):
		h.watchDialogCallback(ctx, q, m)
	case strings.HasPrefix(q.Data, "rc:"):
		h.recoveryCallback(ctx, q, m)
	case strings.HasPrefix(q.Data, "sc:"):
		h.scheduleCallback(ctx, q, m)
	case strings.HasPrefix(q.Data, "dm:"):
		h.dailyMenuCallback(ctx, q, m)
	case strings.HasPrefix(q.Data, "nav:"):
		h.navigationCallback(ctx, q, m)
	default:
		return false
	}
	return true
}

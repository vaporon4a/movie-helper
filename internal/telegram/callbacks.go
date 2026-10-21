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
	if strings.HasPrefix(query.Data, "fv:") && h.featureCallback(ctx, query, message) {
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

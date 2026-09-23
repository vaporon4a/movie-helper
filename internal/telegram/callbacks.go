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
	if message == nil || !h.Allowed[message.Chat.ID] || query.From.IsBot {
		return
	}
	_, _ = h.API.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: query.ID})
	if raw, ok := strings.CutPrefix(query.Data, "movie_more:"); ok {
		roundID, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || h.MovieClub == nil {
			return
		}
		if err = h.MovieClub.More(ctx, message.Chat.ID, roundID); err != nil {
			h.reply(ctx, message.Chat.ID, "Дополнительная подборка уже отправлена или сейчас недоступна.")
		}
		return
	}
	if !h.admin(ctx, message.Chat.ID, query.From.ID) {
		h.reply(ctx, message.Chat.ID, "Одобрять материалы может администратор чата.")
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

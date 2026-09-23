package telegram

import (
	"context"

	"github.com/go-telegram/bot"
)

func (h *Handler) admin(ctx context.Context, chatID, userID int64) bool {
	admins, err := h.API.GetChatAdministrators(ctx, &bot.GetChatAdministratorsParams{ChatID: chatID})
	if err != nil {
		return false
	}
	for _, admin := range admins {
		if admin.Owner != nil && admin.Owner.User.ID == userID {
			return true
		}
		if admin.Administrator != nil && admin.Administrator.User.ID == userID {
			return true
		}
	}
	return false
}

func (h *Handler) reply(ctx context.Context, chatID int64, text string) {
	if _, err := h.API.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: text}); err != nil {
		h.Log.Warn("command reply failed", "chat_id", chatID)
	}
}

func (h *Handler) problem(ctx context.Context, chatID int64) {
	h.Log.Error("storage operation failed", "chat_id", chatID)
	h.reply(ctx, chatID, "Не удалось прочитать настройки. Попробуйте позже.")
}

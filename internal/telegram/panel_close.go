package telegram

import (
	"context"
	"errors"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

const closePanelAction = "ui:close"

// Every interactive panel ends with the same safe action. It closes only the
// message; it does not cancel a viewing, change a vote or stop a review series.
func closableKeyboard(keyboard *models.InlineKeyboardMarkup) *models.InlineKeyboardMarkup {
	if keyboard == nil {
		keyboard = &models.InlineKeyboardMarkup{}
	}
	rows := make([][]models.InlineKeyboardButton, 0, len(keyboard.InlineKeyboard)+1)
	for _, row := range keyboard.InlineKeyboard {
		buttons := make([]models.InlineKeyboardButton, 0, len(row))
		for _, button := range row {
			if button.CallbackData != closePanelAction {
				buttons = append(buttons, button)
			}
		}
		if len(buttons) > 0 {
			rows = append(rows, buttons)
		}
	}
	rows = append(rows, []models.InlineKeyboardButton{{Text: "✖️ Закрыть", CallbackData: closePanelAction}})
	return &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func (h *Handler) panelChatAllowed(m *models.Message, user int64) bool {
	switch m.Chat.Type {
	case models.ChatTypePrivate:
		return m.Chat.ID == user
	case models.ChatTypeGroup, models.ChatTypeSupergroup:
		return h.Allowed[m.Chat.ID]
	case models.ChatTypeChannel:
		return false
	default:
		return false
	}
}

func (h *Handler) closePanelCallback(ctx context.Context, q *models.CallbackQuery, m *models.Message) {
	if !h.panelChatAllowed(m, q.From.ID) || m.ID <= 0 {
		return
	}
	_, _ = h.API.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: q.ID})
	h.Log.DebugContext(ctx, "menu close requested", "chat_id", m.Chat.ID, "message_id", m.ID)
	// Persist before calling Telegram so future background refreshes skip this UI.
	if h.Watches != nil && m.Chat.Type != models.ChatTypePrivate {
		if err := h.Watches.CloseMessage(ctx, m.Chat.ID, m.ID); err != nil {
			h.Log.WarnContext(ctx, "menu close failed", "chat_id", m.Chat.ID, "message_id", m.ID, "reason", "storage")
			return
		}
	}
	if err := h.deleteOrHidePanel(ctx, m.Chat.ID, m.ID); err != nil {
		h.Log.WarnContext(ctx, "menu close failed", "chat_id", m.Chat.ID, "message_id", m.ID, "reason", "telegram_request_failed")
		return
	}
	h.Log.DebugContext(ctx, "menu closed", "chat_id", m.Chat.ID, "message_id", m.ID)
}

func (h *Handler) deleteOrHidePanel(ctx context.Context, chat int64, message int) error {
	ok, err := h.API.DeleteMessage(ctx, &bot.DeleteMessageParams{ChatID: chat, MessageID: message})
	if err == nil {
		if ok {
			return nil
		}
		return errors.New("panel deletion returned no confirmation")
	}
	if !errors.Is(err, bot.ErrorBadRequest) {
		return err
	}
	if strings.Contains(strings.ToLower(err.Error()), "message to delete not found") {
		return nil
	}
	// Telegram rejects deletion of old messages. Removing their keyboard still
	// closes the panel, including photo-based moderation previews.
	_, err = h.API.EditMessageReplyMarkup(ctx, &bot.EditMessageReplyMarkupParams{ChatID: chat, MessageID: message, ReplyMarkup: &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{}}})
	if errors.Is(err, bot.ErrorBadRequest) && strings.Contains(strings.ToLower(err.Error()), "message is not modified") {
		return nil
	}
	return err
}

package telegram

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/vaporon4a/movie-helper/internal/interaction"
)

type requesterKey struct{}

func (h *Handler) deliverPanel(ctx context.Context, chat int64, message int, purpose, text string, keyboard *models.InlineKeyboardMarkup) (int, error) {
	if message > 0 {
		_, err := h.API.EditMessageText(ctx, &bot.EditMessageTextParams{ChatID: chat, MessageID: message, Text: limitMenuText(text), ReplyMarkup: closableKeyboard(keyboard), LinkPreviewOptions: disabledLinkPreview()})
		if isUnmodified(err) {
			err = nil
		}
		return message, err
	}
	if h.Panels == nil {
		return h.sendUnregisteredPanel(ctx, chat, text, keyboard)
	}
	requester, _ := ctx.Value(requesterKey{}).(int64)
	p, err := h.Panels.AcquirePanel(ctx, chat, requester, purpose, h.Now())
	if err != nil {
		return 0, err
	}
	if p.State == "confirmed" {
		return h.reusePanel(ctx, p, text, keyboard)
	}
	if p.State != "claimed" {
		h.Log.WarnContext(ctx, "panel receipt unresolved", "chat_id", chat, "purpose", purpose, "operation_id", p.Token)
		return 0, errResponseSent
	}
	return h.sendClaimedPanel(ctx, p, text, keyboard)
}

func (h *Handler) sendUnregisteredPanel(ctx context.Context, chat int64, text string, keyboard *models.InlineKeyboardMarkup) (int, error) {
	m, err := h.API.SendMessage(ctx, &bot.SendMessageParams{ChatID: chat, Text: limitMenuText(text), ReplyMarkup: closableKeyboard(keyboard), LinkPreviewOptions: disabledLinkPreview()})
	if m != nil {
		return m.ID, err
	}
	return 0, err
}

func (h *Handler) reusePanel(ctx context.Context, p interaction.Panel, text string, keyboard *models.InlineKeyboardMarkup) (int, error) {
	chat, purpose := p.ChatID, p.Purpose
	if h.Interactions != nil && purpose != "watch-dialog" {
		screen := "menu"
		if purpose == "profile" {
			screen = "profile"
		}
		if err := h.Interactions.SetPanelScreen(ctx, chat, p.MessageID, screen); err != nil {
			return 0, err
		}
	}
	id, e := h.deliverPanel(ctx, chat, p.MessageID, purpose, text, keyboard)
	if e != nil && errors.Is(e, bot.ErrorBadRequest) && strings.Contains(strings.ToLower(e.Error()), "message to edit not found") {
		if e = h.Panels.RetirePanel(ctx, p); e != nil {
			return 0, e
		}
		return h.deliverPanel(ctx, chat, 0, purpose, text, keyboard)
	}
	return id, e
}

func (h *Handler) sendClaimedPanel(ctx context.Context, p interaction.Panel, text string, keyboard *models.InlineKeyboardMarkup) (int, error) {
	chat := p.ChatID
	m, sendErr := h.API.SendMessage(ctx, &bot.SendMessageParams{ChatID: chat, Text: limitMenuText(text), ReplyMarkup: closableKeyboard(keyboard), LinkPreviewOptions: disabledLinkPreview()})
	state, id := panelSendOutcome(m, sendErr)
	persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := h.Panels.FinishPanel(persist, p, id, state, h.Now()); err != nil {
		return id, err
	}
	if state == "unknown" {
		return 0, errResponseSent
	}
	return id, sendErr
}

func panelSendOutcome(m *models.Message, err error) (string, int) {
	switch {
	case err != nil:
		if classifyProfileDelivery(err).Kind != "unknown" {
			return "rejected", 0
		}
		return "unknown", 0
	case m == nil || m.ID <= 0:
		return "unknown", 0
	default:
		return "confirmed", m.ID
	}
}

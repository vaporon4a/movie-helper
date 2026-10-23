package telegram

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/vaporon4a/movie-helper/internal/recovery"
)

func (h *Handler) recoveryPanel(ctx context.Context, chat int64, offset int) (menuPanel, error) {
	if h.Recovery == nil {
		return menuPanel{"Восстановление недоступно.", menuBack()}, nil
	}
	issues, err := h.Recovery.Issues(ctx, chat, h.Now())
	if err != nil {
		return menuPanel{}, err
	}
	k := menuBack()
	var b strings.Builder
	b.WriteString("🛠 Проверка операций\n\nАвтоматического повторения неопределённой отправки нет. Сначала проверьте сообщения в чате, затем выберите конкретное действие.\n\n")
	if len(issues) == 0 {
		b.WriteString("Проблемных операций нет.")
	}
	if offset < 0 || offset >= len(issues) {
		offset = 0
	}
	end := min(offset+6, len(issues))
	for _, i := range issues[offset:end] {
		b.WriteString(i.Label + "\n")
		for _, a := range i.Actions {
			k.InlineKeyboard = append(k.InlineKeyboard, []models.InlineKeyboardButton{{Text: clipProfile(fmt.Sprintf("%s #%d · %s", recoveryDomainName(i.Domain), i.ID, a.Label), 64), CallbackData: fmt.Sprintf("rc:prepare:%s:%d:%s", i.Domain, i.ID, a.Key)}})
		}
	}
	if end < len(issues) {
		k.InlineKeyboard = append(k.InlineKeyboard, []models.InlineKeyboardButton{menuButton("Дальше →", fmt.Sprintf("recovery_%d", end))})
	}
	if offset > 0 {
		k.InlineKeyboard = append(k.InlineKeyboard, []models.InlineKeyboardButton{menuButton("← Назад", fmt.Sprintf("recovery_%d", max(0, offset-6)))})
	}
	b.WriteString("\nПредпросмотры мемов и фактов разбираются в меню соответствующей рубрики.")
	return menuPanel{limitMenuText(b.String()), k}, nil
}
func (h *Handler) recoveryCallback(ctx context.Context, q *models.CallbackQuery, m *models.Message) {
	if h.Recovery == nil || !h.Allowed[m.Chat.ID] || m.Chat.Type == models.ChatTypePrivate {
		return
	}
	parts := strings.Split(q.Data, ":")
	if len(parts) < 3 {
		return
	}
	_, _ = h.API.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: q.ID})
	if parts[1] == "apply" && len(parts) == 3 {
		h.applyRecoveryConfirmation(ctx, parts[2], m)
		return
	}
	if parts[1] != "prepare" || len(parts) != 5 {
		return
	}
	id, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil {
		return
	}
	c, err := h.Recovery.Prepare(ctx, recovery.Confirmation{ChatID: m.Chat.ID, PanelID: m.ID, Issue: recovery.Issue{Domain: parts[2], ID: id}, Action: parts[4]}, h.Now())
	if err != nil {
		return
	}
	consequence := ""
	for _, a := range c.Issue.Actions {
		if a.Key == c.Action {
			consequence = a.Consequence
		}
	}
	k := &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{{Text: "Подтвердить решение", CallbackData: "rc:apply:" + c.Token}}, {menuButton("← Отмена", recoveryPage)}}}
	_, _ = h.API.EditMessageText(ctx, &bot.EditMessageTextParams{ChatID: m.Chat.ID, MessageID: m.ID, Text: c.Issue.Label + "\n\n" + consequence + "\n\nПодтвердить?", ReplyMarkup: closableKeyboard(k)})
}

func (h *Handler) applyRecoveryConfirmation(ctx context.Context, token string, m *models.Message) {
	err := h.Recovery.Apply(ctx, token, m.Chat.ID, m.ID, h.Now())
	notice := "✅ Решение сохранено. Переоткройте экран для актуального состояния."
	if err != nil {
		notice = "Действие не применено: состояние изменилось. Откройте актуальный список."
		if !errors.Is(err, recovery.ErrConflict) {
			notice = "Хранилище временно недоступно. Результат не подтверждён."
		}
	} else {
		h.Log.InfoContext(ctx, "recovery action applied", "chat_id", m.Chat.ID, "operation_id", token)
	}
	_, _ = h.API.EditMessageText(ctx, &bot.EditMessageTextParams{ChatID: m.Chat.ID, MessageID: m.ID, Text: notice, ReplyMarkup: closableKeyboard(&models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{menuButton("↻ Проверить операции", recoveryPage)}}})})
}

func recoveryDomainName(domain string) string {
	return map[string]string{"daily": "Публикация", "club": "Напоминание", "movies": "Киноопрос", "ideas": "Идеи", "ui": "Меню"}[domain]
}

package telegram

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/vaporon4a/movie-helper/internal/clubprofile"
	"github.com/vaporon4a/movie-helper/internal/interaction"
)

func (h *Handler) beginWatched(ctx context.Context, chat int64, panel int, user models.User) error {
	if h.WatchDialog == nil || h.Interactions == nil {
		return errors.New("watch dialog unavailable")
	}
	id, err := h.deliverPanel(ctx, chat, panel, "watch-dialog", "🎞 Добавим просмотр\n\nНазвание или ссылка TMDB → проверка фильма и даты → сохранение. До подтверждения просмотр и вопросы не создаются.", nil)
	if err != nil {
		return err
	}
	if err = h.Interactions.SetPanelScreen(ctx, chat, id, "watch_input"); err != nil {
		return err
	}
	if err = h.cancelDailyPanel(ctx, chat, id); err != nil {
		return err
	}
	err = h.promptInput(ctx, interaction.Intent{ChatID: chat, PanelID: id, Kind: watchPage, Action: timeInputAction, ActorID: user.ID}, user, "что посмотрели? Ответьте названием или ссылкой TMDB. Можно добавить дату после |, например Матрица | 2026-10-16. Без даты — сегодня по времени чата.")
	if err == nil {
		return errResponseSent
	}
	return err
}
func watchDialogPresentation(d clubprofile.WatchDraft) (string, *models.InlineKeyboardMarkup) {
	f := d.Candidates[0]
	text := fmt.Sprintf("🎞 Проверим просмотр\n\n%s\nhttps://www.themoviedb.org/movie/%d\n\nДата: %s (%s)\n\nСохранить этот фильм? Для недавнего просмотра вопросы подготовятся автоматически, если включено обсуждение. Старая или неточная дата пополнит только историю. До подтверждения запись не создана.", f.Label(), f.TMDBID, watchDate(d.Input.Date), d.Input.Zone)
	k := &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{{Text: "✅ Сохранить просмотр", CallbackData: fmt.Sprintf("wd:confirm:%d:%d", d.ID, d.Version)}}, {{Text: "📅 Изменить дату", CallbackData: fmt.Sprintf("wd:date:%d:%d", d.ID, d.Version)}, {Text: "← Другой фильм", CallbackData: "wd:start"}}}}
	return text, k
}
func (h *Handler) watchDialogCallback(ctx context.Context, q *models.CallbackQuery, m *models.Message) {
	if h.WatchDialog == nil || !h.Allowed[m.Chat.ID] || m.Chat.Type == models.ChatTypePrivate {
		return
	}
	_, _ = h.API.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: q.ID})
	if q.Data == "wd:start" {
		_ = h.beginWatched(ctx, m.Chat.ID, m.ID, q.From)
		return
	}
	parts := strings.Split(q.Data, ":")
	if len(parts) != 4 {
		return
	}
	id, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return
	}
	version, err := strconv.Atoi(parts[3])
	if err != nil {
		return
	}
	d, err := h.WatchDialog.Dialog(ctx, m.Chat.ID, id, m.ID)
	if err != nil || d.Version != version {
		return
	}
	if parts[1] == "date" {
		_ = h.cancelDailyPanel(ctx, m.Chat.ID, m.ID)
		_ = h.promptInput(ctx, interaction.Intent{ChatID: m.Chat.ID, PanelID: m.ID, Kind: watchPage, Action: timeInputAction, ActorID: q.From.ID, Revision: id, Clock: strconv.Itoa(version)}, q.From, "когда смотрели? Ответьте датой YYYY-MM-DD, месяцем YYYY-MM, годом или unknown. Будущую дату указать нельзя.")
		return
	}
	if parts[1] != "confirm" {
		return
	}
	d, err = h.WatchDialog.ConfirmDialog(ctx, m.Chat.ID, id, version, "")
	if err != nil {
		h.Log.WarnContext(ctx, "watch confirmation declined", "chat_id", m.Chat.ID, "draft_id", id, "reason", "state_or_storage")
		return
	}
	_ = h.cancelDailyPanel(ctx, m.Chat.ID, m.ID)
	_ = h.EditWatchCard(ctx, d)
}
func (h *Handler) handleWatchDialogReply(ctx context.Context, u *models.Update) bool {
	m := u.Message
	if h.WatchDialog == nil || h.Interactions == nil || m.From == nil || m.ReplyToMessage == nil {
		return false
	}
	i, err := h.Interactions.Reply(ctx, m.Chat.ID, m.From.ID, m.ReplyToMessage.ID, h.Now())
	if err != nil || i.Kind != watchPage {
		return false
	}
	var d clubprofile.WatchDraft
	if i.Revision == 0 {
		d, err = h.WatchDialog.BeginDialog(ctx, u.ID, m.Chat.ID, i.PanelID, strings.TrimSpace(m.Text), i.Token)
	} else {
		version, e := strconv.Atoi(i.Clock)
		if e != nil {
			return true
		}
		d, err = h.WatchDialog.DateDialog(ctx, m.Chat.ID, i.Revision, version, strings.TrimSpace(m.Text))
	}
	if errors.Is(err, clubprofile.ErrInvalid) {
		_, _ = h.API.EditMessageText(ctx, &bot.EditMessageTextParams{ChatID: i.ChatID, MessageID: i.PromptID, Text: "Проверьте название, ссылку TMDB или дату. Ответьте ещё раз на это сообщение; будущая дата недопустима."})
		return true
	}
	if err != nil {
		h.Log.WarnContext(ctx, "watch input failed", "chat_id", i.ChatID, "reason", "state_or_catalog")
		return true
	}
	_ = h.cancelDailyPanel(ctx, i.ChatID, i.PanelID)
	_ = h.EditWatchCard(ctx, d)
	return true
}

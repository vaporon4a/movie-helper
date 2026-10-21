package telegram

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/vaporon4a/movie-helper/internal/clubprofile"
)

const watchedCommand = "/movie_watched"

var errWatchCardRetryScheduled = errors.New("watch card retry scheduled")

func (h *Handler) handleWatchCommand(ctx context.Context, u *models.Update, command, args string) (bool, error) {
	chat := u.Message.Chat.ID
	switch command {
	case watchedCommand:
		return true, h.addWatched(ctx, u, args)
	case "/movie_profile", "/movie_taste":
		return true, h.showClubProfile(ctx, chat, 0, "overview", 0)
	case "/movie_auto_review":
		if args != "on" && args != "off" {
			h.reply(ctx, chat, "/movie_auto_review on или off — автоматические вопросы для новых просмотров. Включение не перезапускает завершённые и остановленные серии.")
			return true, errResponseSent
		}
		err := h.Watches.AutoReview(ctx, u.ID, chat, args == "on")
		if err != nil {
			return true, err
		}
		h.reply(ctx, chat, "Настройка автоматического обсуждения сохранена. Обычные опросы и их расписание не изменились.")
		return true, errResponseSent
	case "/movie_watch_date":
		id, date, ok := strings.Cut(strings.TrimSpace(args), " ")
		n, err := strconv.ParseInt(id, 10, 64)
		if !ok || err != nil {
			h.reply(ctx, chat, "/movie_watch_date ID YYYY-MM-DD — исправить дату. Можно указать год, месяц или unknown. Уже собранные ответы сохранятся; дальнейшие вопросы этой серии остановятся.")
			return true, errResponseSent
		}
		if err = h.Watches.Change(ctx, u.ID, chat, n, "date", date); err != nil {
			return true, err
		}
		return true, h.showClubProfile(ctx, chat, 0, "watch", n)
	default:
		return false, nil
	}
}

func (h *Handler) sendWatchCard(ctx context.Context, d clubprofile.WatchDraft) error {
	if d.MessageID != 0 {
		if d.MessageID > 0 {
			if err := h.EditWatchCard(ctx, d); err != nil {
				return err
			}
		}
		return errResponseSent
	}
	text, keyboard, err := h.watchDraftCard(ctx, d)
	if err != nil {
		return err
	}
	claimed, err := h.Watches.ClaimMessage(ctx, d.ChatID, d.ID)
	if err != nil {
		return err
	}
	if !claimed {
		return errResponseSent
	}
	m, err := h.API.SendMessage(ctx, &bot.SendMessageParams{ChatID: d.ChatID, Text: text, ReplyMarkup: keyboard, LinkPreviewOptions: &models.LinkPreviewOptions{IsDisabled: new(true)}})
	persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err != nil {
		return h.persistWatchCardFailure(persist, d, err)
	}
	if m == nil || m.ID <= 0 {
		return clubprofile.ErrInvalid
	}
	if err = h.Watches.BindMessage(persist, d.ChatID, d.ID, m.ID); err != nil {
		return err
	}
	return errResponseSent
}

func (h *Handler) persistWatchCardFailure(ctx context.Context, d clubprofile.WatchDraft, err error) error {
	failure := classifyProfileDelivery(err)
	if failure.Kind != "retry" {
		return failure
	}
	if err = h.Watches.RetryMessage(ctx, d.ChatID, d.ID, failure.After); err != nil {
		return err
	}
	h.Log.InfoContext(ctx, "watch card retry scheduled", "chat_id", d.ChatID, "draft_id", d.ID, "retry_after", failure.After)
	return errors.Join(errWatchCardRetryScheduled, failure)
}

func (h *Handler) EditWatchCard(ctx context.Context, d clubprofile.WatchDraft) error {
	text, keyboard, err := h.watchDraftCard(ctx, d)
	if err != nil {
		return err
	}
	_, err = h.API.EditMessageText(ctx, &bot.EditMessageTextParams{ChatID: d.ChatID, MessageID: d.MessageID, Text: text, ReplyMarkup: keyboard, LinkPreviewOptions: &models.LinkPreviewOptions{IsDisabled: new(true)}})
	if errors.Is(err, bot.ErrorBadRequest) && strings.Contains(strings.ToLower(err.Error()), "message is not modified") {
		return nil
	}
	if err != nil {
		return classifyProfileDelivery(err)
	}
	return nil
}

func (h *Handler) watchDraftCard(ctx context.Context, d clubprofile.WatchDraft) (string, *models.InlineKeyboardMarkup, error) {
	keyboard := &models.InlineKeyboardMarkup{}
	switch d.State {
	case "saved":
		return h.savedWatchCard(ctx, d)
	case "choice":
		for _, f := range d.Candidates {
			keyboard.InlineKeyboard = append(keyboard.InlineKeyboard, []models.InlineKeyboardButton{{Text: clipProfile(f.Label(), 70), CallbackData: fmt.Sprintf("cp:choose:%d:%d", d.ID, f.TMDBID)}})
		}
		return fmt.Sprintf("🎬 Какой фильм посмотрели?\nДата: %s\n\nВыберите один вариант — просмотр сразу сохранится. Выбор действует до %s.", watchDate(d.Input.Date), d.Expires.Format("02.01 15:04 MST")), keyboard, nil
	case "expired":
		return "Поиск просмотра остановлен: прошли сутки. Ничего не записано. Отправьте /movie_watched ещё раз с точной ссылкой TMDB.", keyboard, nil
	case "not_found":
		return "Фильм не найден. Попробуйте другое название или точную ссылку TMDB через /movie_watched.", keyboard, nil
	default:
		return fmt.Sprintf("🎬 Заявка #%d сохранена. Уточняю фильм в TMDB.\nЕсли сервис недоступен, повторю в фоне до %s. Пока подтверждённого просмотра и опросов нет.", d.ID, d.Expires.Format("02.01 15:04 MST")), keyboard, nil
	}
}

func (h *Handler) savedWatchCard(ctx context.Context, d clubprofile.WatchDraft) (string, *models.InlineKeyboardMarkup, error) {
	view, err := h.loadProfileView(ctx, d.ChatID)
	if err != nil {
		return "", nil, err
	}
	text, keyboard := view.watchCard(d.WatchID)
	return "✅ Просмотр сохранён\n\n" + text, keyboard, nil
}

func watchDate(date string) string {
	if date == "" {
		return "неизвестна"
	}
	return date
}
func clipProfile(text string, n int) string {
	r := []rune(text)
	if len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return text
}

func (h *Handler) profileCallback(ctx context.Context, operation int64, data string, m *models.Message) bool {
	if !strings.HasPrefix(data, "cp:") || h.Watches == nil {
		return false
	}
	parts := strings.Split(data, ":")
	if len(parts) < 3 {
		return true
	}
	err := h.applyProfileCallback(ctx, operation, parts, m)
	if err != nil && !errors.Is(err, errResponseSent) {
		h.reply(ctx, m.Chat.ID, "Запись недоступна, выбор устарел или операция уже выполнена. Актуальные данные: /movie_profile")
	}
	return true
}

func (h *Handler) applyProfileCallback(ctx context.Context, operation int64, p []string, m *models.Message) error {
	if p[1] == "view" && len(p) == 4 {
		n, err := strconv.ParseInt(p[3], 10, 64)
		if err != nil {
			return err
		}
		return h.showClubProfile(ctx, m.Chat.ID, m.ID, p[2], n)
	}
	id, err := strconv.ParseInt(p[2], 10, 64)
	if err != nil {
		return err
	}
	switch p[1] {
	case "choose":
		if len(p) != 4 {
			return clubprofile.ErrInvalid
		}
		film, e := strconv.ParseInt(p[3], 10, 64)
		if e != nil {
			return e
		}
		d, e := h.Watches.Choose(ctx, m.Chat.ID, id, film)
		if e != nil {
			return e
		}
		return h.EditWatchCard(ctx, d)
	case "pause", "annul":
		if err = h.Watches.Change(ctx, operation, m.Chat.ID, id, p[1], ""); err != nil {
			return err
		}
		return h.showClubProfile(ctx, m.Chat.ID, m.ID, "watch", id)
	case "edit":
		h.reply(ctx, m.Chat.ID, fmt.Sprintf("Исправить дату: /movie_watch_date %d YYYY-MM-DD\nДля замены фильма: отмените запись в карточке и добавьте правильную через /movie_watched. История исправлений сохраняется.", id))
		return nil
	default:
		return clubprofile.ErrInvalid
	}
}

func (h *Handler) addWatched(ctx context.Context, u *models.Update, args string) error {
	chat := u.Message.Chat.ID
	if strings.TrimSpace(args) == "" {
		h.reply(ctx, chat, "🎬 Что посмотрели?\n/movie_watched https://www.themoviedb.org/movie/603\n/movie_watched Матрица | 2026-10-16\n\nБез даты — сегодня по времени чата. Для истории допустимы год, месяц или unknown. Ссылка записывает просмотр сразу, название предложит варианты.")
		return errResponseSent
	}
	d, err := h.Watches.Add(ctx, u.ID, chat, args)
	if errors.Is(err, clubprofile.ErrInvalid) {
		h.reply(ctx, chat, "Проверьте название или ссылку TMDB на фильм и дату после |. Будущие даты не принимаются.")
		return errResponseSent
	}
	if err != nil {
		return err
	}
	err = h.sendWatchCard(ctx, d)
	if errors.Is(err, errWatchCardRetryScheduled) {
		// The card is already scheduled; do not add a separate error message to the chat.
		return errResponseSent
	}
	return err
}

// PublishWatchCard handles both a confirmed retry of the first card and subsequent edits.
func (h *Handler) PublishWatchCard(ctx context.Context, d clubprofile.WatchDraft) error {
	err := h.sendWatchCard(ctx, d)
	if errors.Is(err, errResponseSent) {
		return nil
	}
	return err
}

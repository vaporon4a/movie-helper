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
	"github.com/vaporon4a/movie-helper/internal/daily"
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
		return true, h.showClubProfile(ctx, chat, 0, watchPage, n)
	default:
		return false, nil
	}
}

func (h *Handler) sendWatchCard(ctx context.Context, d clubprofile.WatchDraft) error {
	if d.Closed {
		return errResponseSent
	}
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
	keyboard = closableKeyboard(keyboard)
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
	if h.Interactions != nil {
		screen, err := h.Interactions.PanelScreen(ctx, d.ChatID, d.MessageID)
		if err != nil {
			return err
		}
		if screen != "" && screen != fmt.Sprintf("watch:%d", d.ID) && screen != "profile" {
			return nil
		}
	}
	if d.Closed {
		return nil
	}
	text, keyboard, closed, err := h.currentWatchPanel(ctx, d)
	if err != nil {
		return err
	}
	if closed {
		return nil
	}
	keyboard = closableKeyboard(keyboard)
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
		if d.Input.Provisional && d.Reason == "confirm_required" && len(d.Candidates) == 1 {
			text, k := watchDialogPresentation(d)
			return text, k, nil
		}
		for _, f := range d.Candidates {
			keyboard.InlineKeyboard = append(keyboard.InlineKeyboard, []models.InlineKeyboardButton{{Text: clipProfile(f.Label(), 70), CallbackData: fmt.Sprintf("cp:choose:%d:%d", d.ID, f.TMDBID)}})
		}
		if d.Input.Provisional {
			return "🎬 Выберите фильм. Сохранение будет только после отдельного подтверждения даты и названия.", keyboard, nil
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
	if !h.profileActionAllowed(ctx, data, m) {
		return true
	}
	parts := strings.Split(data, ":")
	if len(parts) < 3 {
		return true
	}
	err := h.applyProfileCallback(ctx, operation, parts, m)
	if err != nil && !errors.Is(err, errResponseSent) {
		_, _ = h.API.EditMessageText(ctx, &bot.EditMessageTextParams{ChatID: m.Chat.ID, MessageID: m.ID, Text: "Действие не применено: запись изменилась, выбор устарел или операция уже выполнена. Откройте актуальную историю.", ReplyMarkup: closableKeyboard(profileNavigation())})
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
		return h.chooseProfileFilm(ctx, p, m, id)
	case "pause_confirm":
		return h.confirmProfilePause(ctx, operation, p, m, id)
	case menuPauseAction, editPage:
		return h.showClubProfile(ctx, m.Chat.ID, m.ID, p[1], id)
	case "annul":
		// Old Telegram cards outlive releases. Their cancel buttons must also
		// become harmless; hiding the button in new cards is not sufficient.
		return h.showClubProfile(ctx, m.Chat.ID, m.ID, watchPage, id)
	default:
		return clubprofile.ErrInvalid
	}
}

func (h *Handler) addWatched(ctx context.Context, u *models.Update, args string) error {
	chat := u.Message.Chat.ID
	if strings.TrimSpace(args) == "" {
		if h.WatchDialog != nil && u.Message.From != nil {
			return h.beginWatched(ctx, chat, 0, *u.Message.From)
		}
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

func (h *Handler) currentWatchPanel(ctx context.Context, d clubprofile.WatchDraft) (string, *models.InlineKeyboardMarkup, bool, error) {
	if h.ProfilePanels == nil {
		return h.draftPanel(ctx, d)
	}
	page, id, closed, err := h.ProfilePanels.ProfilePage(ctx, d.ChatID, d.MessageID)
	if err != nil || closed {
		return "", nil, closed, err
	}
	if d.State != "saved" || page == "" || (page == watchPage && id == 0) {
		return h.draftPanel(ctx, d)
	}
	view, err := h.loadProfileView(ctx, d.ChatID)
	if err != nil {
		return "", nil, false, err
	}
	expected, err := h.ProfilePanels.ProfilePageRevision(ctx, d.ChatID, d.MessageID)
	if err != nil {
		return "", nil, false, err
	}
	text, k := view.stableWatchPage(page, id, expected)
	return text, k, false, nil
}
func (h *Handler) draftPanel(ctx context.Context, d clubprofile.WatchDraft) (string, *models.InlineKeyboardMarkup, bool, error) {
	h.Log.DebugContext(ctx, "watch panel refreshed", "chat_id", d.ChatID, "draft_id", d.ID, "version", d.Version)
	text, k, err := h.watchDraftCard(ctx, d)
	return text, k, false, err
}
func (v clubProfileView) stableWatchPage(page string, id int64, expected int) (string, *models.InlineKeyboardMarkup) {
	if page == menuPauseAction {
		for _, w := range v.watches {
			if w.ID == id && w.Revision != expected {
				text, k := v.watchCard(id)
				return "Настройки просмотра изменились. Старое подтверждение отменено.\n\n" + text, k
			}
		}
	}
	return v.page(page, id)
}

func (h *Handler) chooseProfileFilm(ctx context.Context, p []string, m *models.Message, id int64) error {
	if len(p) != 4 {
		return clubprofile.ErrInvalid
	}
	film, err := strconv.ParseInt(p[3], 10, 64)
	if err != nil {
		return err
	}
	d, err := h.Watches.Choose(ctx, m.Chat.ID, id, film)
	if err != nil {
		return err
	}
	return h.EditWatchCard(ctx, d)
}

func (h *Handler) confirmProfilePause(ctx context.Context, op int64, p []string, m *models.Message, id int64) error {
	if len(p) != 4 {
		return h.showClubProfile(ctx, m.Chat.ID, m.ID, watchPage, id)
	}
	err := h.Watches.Change(ctx, op, m.Chat.ID, id, menuPauseAction, p[3])
	if err != nil && !errors.Is(err, daily.ErrDuplicate) && !errors.Is(err, clubprofile.ErrDuplicate) {
		return err
	}
	return h.showClubProfile(ctx, m.Chat.ID, m.ID, watchPage, id)
}

func (h *Handler) profileActionAllowed(ctx context.Context, data string, m *models.Message) bool {
	if !h.profileScreenMatches(ctx, data, m) {
		return false
	}
	if h.ProfilePanels != nil {
		page, _, closed, err := h.ProfilePanels.ProfilePage(ctx, m.Chat.ID, m.ID)
		if err != nil || closed {
			return false
		}
		if strings.HasPrefix(data, "cp:pause_confirm:") && page != "" && page != menuPauseAction {
			return false
		}
	}
	return true
}

func (h *Handler) profileScreenMatches(ctx context.Context, data string, m *models.Message) bool {
	if h.Interactions != nil && !strings.HasPrefix(data, "cp:view:") {
		screen, err := h.Interactions.PanelScreen(ctx, m.Chat.ID, m.ID)
		if err != nil {
			return false
		}
		if owner, ok := strings.CutPrefix(screen, "watch:"); ok {
			return strings.HasPrefix(data, "cp:choose:"+owner+":")
		}
		if screen != "" && screen != "profile" {
			return false
		}
	}

	return true
}

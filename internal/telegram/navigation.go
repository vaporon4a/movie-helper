package telegram

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

type menuPanel struct {
	text     string
	keyboard *models.InlineKeyboardMarkup
}

func menuButton(text, page string) models.InlineKeyboardButton {
	return models.InlineKeyboardButton{Text: text, CallbackData: "nav:" + page}
}

func menuBack() *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{
		{menuButton("← Настройки", "home"), menuButton("🏠 Помощь", "help")},
	}}
}

func (h *Handler) showMenu(ctx context.Context, chat int64, message int, page string, private bool) error {
	h.Log.DebugContext(ctx, "menu requested", "chat_id", chat, "page", page, "message_id", message)
	if err := h.prepareMenuPanel(ctx, chat, message, page, private); err != nil {
		return err
	}
	if strings.HasPrefix(page, "schedule_") && !private {
		return h.beginSchedule(ctx, chat, message, strings.TrimPrefix(page, "schedule_"))
	}
	if strings.HasPrefix(page, "daily_") && !private {
		return h.dailyKindPanel(ctx, chat, message, strings.TrimPrefix(page, "daily_"), "", false)
	}
	p, err := h.menuPage(ctx, chat, page, private)
	if err != nil {
		return err
	}
	p.text = limitMenuText(p.text)
	p.keyboard = closableKeyboard(p.keyboard)
	purpose := "settings"
	if menuScreen(page) == recoveryPage {
		purpose = recoveryPage
	}
	_, err = h.deliverPanel(ctx, chat, message, purpose, p.text, p.keyboard)
	if errors.Is(err, bot.ErrorBadRequest) && strings.Contains(strings.ToLower(err.Error()), "message is not modified") {
		return errResponseSent
	}
	if err != nil {
		h.Log.WarnContext(ctx, "menu delivery failed", "chat_id", chat, "page", page, "reason", "telegram_request_failed")
		return err
	}
	h.Log.DebugContext(ctx, "menu rendered", "chat_id", chat, "page", page)
	return errResponseSent
}

func (h *Handler) navigationCallback(ctx context.Context, q *models.CallbackQuery, m *models.Message) {
	page := strings.TrimPrefix(q.Data, "nav:")
	private := m.Chat.Type == models.ChatTypePrivate
	if !h.panelChatAllowed(m, q.From.ID) {
		return
	}
	if private && page != "help" && page != "about" && page != "home" {
		return
	}
	if !knownMenuPage(page) {
		return
	}
	_, _ = h.API.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: q.ID})
	if err := h.showMenu(ctx, m.Chat.ID, m.ID, page, private); err != nil && !errors.Is(err, errResponseSent) {
		h.Log.WarnContext(ctx, "menu page failed", "chat_id", m.Chat.ID, "page", page, "reason", "unavailable")
	}
}

func knownMenuPage(page string) bool {
	if raw, ok := strings.CutPrefix(page, "recovery_"); ok {
		n, e := strconv.Atoi(raw)
		return e == nil && n >= 0 && n < 1000
	}
	if raw, ok := strings.CutPrefix(page, "agenda_"); ok {
		n, e := strconv.Atoi(raw)
		return e == nil && n >= 0 && n < 1000
	}
	switch page {
	case recoveryPage, "help", "about", "home", "timezone", dailyPage, "movies", "club", "ideas", "backlog", "advanced", "moderation", "diagnostics", "movie_state", "daily_meme", "daily_fact", "schedule_genre", "schedule_reference", "schedule_club", "schedule_ideas":
		return true
	default:
		return false
	}
}

func (h *Handler) menuPage(ctx context.Context, chat int64, page string, private bool) (menuPanel, error) {
	keyboard := menuBack()
	if raw, ok := strings.CutPrefix(page, "recovery_"); ok {
		offset, _ := strconv.Atoi(raw)
		return h.recoveryPanel(ctx, chat, offset)
	}
	if raw, ok := strings.CutPrefix(page, "agenda_"); ok {
		offset, _ := strconv.Atoi(raw)
		return h.agendaPanel(ctx, chat, offset)
	}
	switch page {
	case "help":
		text := help
		if private {
			text += "\n\nОтправляйте команды в подключённой группе киноклуба: у каждого чата свои настройки и история."
		}
		return menuPanel{text, &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{
			{menuButton("⚙️ Настройки", "home"), menuButton("ℹ️ О боте", "about")},
		}}}, nil
	case "about":
		return menuPanel{about, &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{menuButton("← Помощь", "help"), menuButton("⚙️ Настройки", "home")}}}}, nil
	case "home":
		if private {
			return menuPanel{"⚙️ Настройки принадлежат группе киноклуба. Откройте /settings в подключённой группе — там же будут её расписания и история.", keyboard}, nil
		}
		return h.settingsHome(ctx, chat)
	case "timezone":
		return menuPanel{"🌍 Часовой пояс чата\n\nВсе расписания работают по времени чата. Чтобы изменить его, отправьте команду с вашим городом:\n\n/timezone Europe/Moscow\n/timezone Asia/Novosibirsk\n\nПосле изменения проверьте время в разделах настроек.", keyboard}, nil
	case dailyPage:
		return h.dailyMenu(ctx, chat)
	case "movies":
		return h.moviesMenu(ctx, chat)
	case "club":
		return h.clubMenu(ctx, chat)
	case "ideas":
		return h.ideasMenu(ctx, chat)
	case "backlog":
		return h.backlogMenu(ctx, chat)
	case "advanced":
		keyboard.InlineKeyboard = append([][]models.InlineKeyboardButton{
			{menuButton("📥 Ручная модерация", "moderation")},
			{menuButton("🛠 Диагностика", "diagnostics")},
			{menuButton("🧯 Проверить операции", recoveryPage)},
		}, keyboard.InlineKeyboard...)
		return menuPanel{helpManage, keyboard}, nil
	case "moderation":
		return menuPanel{moderationHelp, &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{menuButton("← Дополнительно", "advanced")}}}}, nil
	case recoveryPage:
		return h.recoveryPanel(ctx, chat, 0)
	case "diagnostics":
		return h.diagnosticsMenu(ctx, chat)
	case "movie_state":
		return h.movieStateMenu(ctx, chat)
	default:
		return menuPanel{}, errors.New("unknown menu page")
	}
}

// Keep long diagnostic histories and user-written idea lists within Telegram's
// message limit, including emoji represented by two UTF-16 code units.
func limitMenuText(text string) string {
	units := utf16.Encode([]rune(text))
	if len(units) <= 3900 {
		return text
	}
	end := 3850
	if units[end-1] >= 0xd800 && units[end-1] <= 0xdbff {
		end--
	}
	return string(utf16.Decode(units[:end])) + "…\n\nПоказана часть данных."
}

// CoreCommands is the small public menu; other commands remain available in
// their settings section and continue to work when entered directly.
func CoreCommands() []models.BotCommand {
	return []models.BotCommand{
		{Command: "movie_watched", Description: "Добавить просмотренный фильм"},
		{Command: "movie_profile", Description: "История, впечатления и интересы клуба"},
		{Command: "idea", Description: "Предложить улучшение бота"},
		{Command: "my_ideas", Description: "Ваши активные предложения"},
		{Command: "settings", Description: "Расписания и настройки"},
	}
}

func (h *Handler) prepareMenuPanel(ctx context.Context, chat int64, message int, page string, private bool) error {
	if h.Interactions != nil && message > 0 && !private {
		closed, err := h.Interactions.Closed(ctx, chat, message)
		if err != nil {
			return err
		}
		if closed {
			return errResponseSent
		}
	}
	if err := h.cancelDailyPanel(ctx, chat, message); err != nil {
		return err
	}
	if h.Interactions != nil && message > 0 && !private {
		screen := menuScreen(page)
		if err := h.Interactions.SetPanelScreen(ctx, chat, message, screen); err != nil {
			return err
		}
	}
	return nil
}

func menuScreen(page string) string {
	switch {
	case strings.HasPrefix(page, "schedule_"):
		return "schedule"
	case strings.HasPrefix(page, "daily_"):
		return dailyPage
	case page == recoveryPage || strings.HasPrefix(page, "recovery_"):
		return recoveryPage
	default:
		return "menu"
	}
}

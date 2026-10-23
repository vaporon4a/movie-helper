package telegram

import (
	"context"
	"errors"
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
	p, err := h.menuPage(ctx, chat, page, private)
	if err != nil {
		return err
	}
	p.text = limitMenuText(p.text)
	if message == 0 {
		_, err = h.API.SendMessage(ctx, &bot.SendMessageParams{ChatID: chat, Text: p.text, ReplyMarkup: p.keyboard, LinkPreviewOptions: disabledLinkPreview()})
	} else {
		_, err = h.API.EditMessageText(ctx, &bot.EditMessageTextParams{ChatID: chat, MessageID: message, Text: p.text, ReplyMarkup: p.keyboard, LinkPreviewOptions: disabledLinkPreview()})
	}
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
	private := m.Chat.Type == models.ChatTypePrivate && m.Chat.ID == q.From.ID
	group := (m.Chat.Type == models.ChatTypeGroup || m.Chat.Type == models.ChatTypeSupergroup) && h.Allowed[m.Chat.ID]
	if !private && !group {
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
	switch page {
	case "help", "about", "home", "timezone", "daily", "movies", "club", "ideas", "backlog", "advanced", "moderation", "diagnostics", "movie_state":
		return true
	default:
		return false
	}
}

func (h *Handler) menuPage(ctx context.Context, chat int64, page string, private bool) (menuPanel, error) {
	keyboard := menuBack()
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
		return menuPanel{about, keyboard}, nil
	case "home":
		if private {
			return menuPanel{"⚙️ Настройки принадлежат группе киноклуба. Откройте /settings в подключённой группе — там же будут её расписания и история.", keyboard}, nil
		}
		return h.settingsHome(ctx, chat)
	case "timezone":
		return menuPanel{"🌍 Часовой пояс чата\n\nВсе расписания работают по времени чата. Чтобы изменить его, отправьте команду с вашим городом:\n\n/timezone Europe/Moscow\n/timezone Asia/Novosibirsk\n\nПосле изменения проверьте время в разделах настроек.", keyboard}, nil
	case "daily":
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
		}, keyboard.InlineKeyboard...)
		return menuPanel{helpManage, keyboard}, nil
	case "moderation":
		return menuPanel{moderationHelp, keyboard}, nil
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

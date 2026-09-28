package telegram

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/vaporon4a/movie-helper/internal/featurevote"
)

const featurePageSize = 5

type FeatureSender struct {
	API      API
	Username string
}

func (s FeatureSender) OpenRound(ctx context.Context, round featurevote.Round, payload string) (int, error) {
	title := "💡 <b>Выбираем следующую функцию бота</b>"
	lead := "Откройте список, прочитайте описания и выберите одну идею. Голос можно изменить до закрытия."
	if round.ParentID != 0 {
		title = "⚖️ <b>Второй тур голосования</b>"
		lead = "Лидеры набрали поровну. Выберите одну идею во втором туре."
	}
	text := fmt.Sprintf("%s\n\n%s\n\nВариантов: %d · закрытие через %s\nПобедитель попадёт в бэклог разработки.", title, lead, len(round.Options), time.Until(time.Unix(round.ClosesAt, 0)).Round(time.Minute))
	link := "https://t.me/" + s.Username + "?start=" + url.QueryEscape(payload)
	message, err := s.API.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: round.ChatID, Text: text, ParseMode: models.ParseModeHTML,
		ReplyMarkup: &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{{Text: "🗳 Голосовать и читать идеи", URL: link}}}},
	})
	if err != nil {
		return 0, classifyFeature(err)
	}
	if message == nil {
		return 0, &featurevote.DeliveryError{Kind: featurevote.DeliveryUnknown, Reason: "empty_message"}
	}
	return message.ID, nil
}

func (s FeatureSender) SendResult(ctx context.Context, round featurevote.Round) error {
	text := "💡 <b>Голосование завершено</b>\n\nНикто не проголосовал. Все идеи остаются в следующем раунде."
	switch round.Outcome {
	case "winner":
		winner := featureOption(round.Options, round.WinnerID)
		text = fmt.Sprintf("🏆 <b>Выбрана следующая функция</b>\n\n<b>%s</b>\n%s\n\nГолосов: %d\nИдея добавлена в бэклог разработки.", html.EscapeString(winner.Title), html.EscapeString(winner.Text), winner.Votes)
	case "tie":
		leaders, votes := featureLeaders(round.Options)
		text = fmt.Sprintf("⚖️ <b>Голоса разделились поровну</b>\n\nЛидеров: %d · по %d голосов.\nБот запускает 12-часовой второй тур только между ними.", leaders, votes)
	case "tie_final":
		leaders, votes := featureLeaders(round.Options)
		text = fmt.Sprintf("⚖️ <b>Второй тур завершился вничью</b>\n\nЛидеров: %d · по %d голосов. Победителя нет.\nВсе идеи остаются в следующих еженедельных голосованиях.", leaders, votes)
	}
	_, err := s.API.SendMessage(ctx, &bot.SendMessageParams{ChatID: round.ChatID, Text: text, ParseMode: models.ParseModeHTML})
	if err != nil {
		return classifyFeature(err)
	}
	return nil
}

func classifyFeature(err error) error {
	var migrate *bot.MigrateError
	if rate, ok := errors.AsType[*bot.TooManyRequestsError](err); ok {
		return &featurevote.DeliveryError{Kind: featurevote.DeliveryRetry, After: time.Duration(rate.RetryAfter) * time.Second, Reason: "rate_limit"}
	}
	if errors.Is(err, bot.ErrorForbidden) || errors.Is(err, bot.ErrorUnauthorized) || errors.As(err, &migrate) {
		return &featurevote.DeliveryError{Kind: featurevote.DeliveryForbidden, Reason: "forbidden"}
	}
	if errors.Is(err, bot.ErrorBadRequest) || errors.Is(err, bot.ErrorNotFound) {
		return &featurevote.DeliveryError{Kind: featurevote.DeliveryPermanent, Reason: telegramRejectionReason(err)}
	}
	return &featurevote.DeliveryError{Kind: featurevote.DeliveryUnknown, Reason: "transport_unknown"}
}

func (h *Handler) sendFeatureList(ctx context.Context, chatID, userID int64, token string, page, messageID int) {
	view, err := h.Features.View(ctx, token, userID)
	if err != nil {
		h.reply(ctx, chatID, "Голосование не найдено или уже недоступно.")
		return
	}
	if !h.featureMember(ctx, view.Round.ChatID, userID) {
		h.reply(ctx, chatID, "Голосование доступно только участникам чата, где оно опубликовано.")
		return
	}
	text, keyboard := featureListView(view, token, page)
	h.sendOrEditFeatureView(ctx, chatID, messageID, text, keyboard)
}

func (h *Handler) sendFeatureDetail(ctx context.Context, chatID, userID int64, token string, ideaID int64, page, messageID int) {
	view, err := h.Features.View(ctx, token, userID)
	if err != nil {
		h.reply(ctx, chatID, "Голосование не найдено или уже недоступно.")
		return
	}
	if !h.featureMember(ctx, view.Round.ChatID, userID) {
		h.reply(ctx, chatID, "Голосование доступно только участникам чата, где оно опубликовано.")
		return
	}
	option := featureOption(view.Options, ideaID)
	if option.IdeaID == 0 {
		return
	}
	text, keyboard := featureDetailView(view, token, option, page)
	h.sendOrEditFeatureView(ctx, chatID, messageID, text, keyboard)
}

func (h *Handler) sendOrEditFeatureView(ctx context.Context, chatID int64, messageID int, text string, keyboard *models.InlineKeyboardMarkup) {
	if messageID == 0 {
		_, err := h.API.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: text, ParseMode: models.ParseModeHTML, ReplyMarkup: keyboard, LinkPreviewOptions: disabledLinkPreview()})
		if err != nil {
			h.Log.Warn("feature private view failed")
		}
		return
	}
	_, err := h.API.EditMessageText(ctx, &bot.EditMessageTextParams{ChatID: chatID, MessageID: messageID, Text: text, ParseMode: models.ParseModeHTML, ReplyMarkup: keyboard, LinkPreviewOptions: disabledLinkPreview()})
	if err != nil {
		h.Log.Warn("feature private view edit failed")
	}
}

func featureListView(view featurevote.View, token string, page int) (string, *models.InlineKeyboardMarkup) {
	pages := max(1, (len(view.Options)+featurePageSize-1)/featurePageSize)
	page = min(max(page, 0), pages-1)
	state := "Голосование открыто"
	if view.Round.State != featurevote.RoundOpen {
		state = "Голосование завершено"
	}
	lines := []string{"💡 <b>Идеи для следующей функции</b>", html.EscapeString(state), "", "Нажмите на название, чтобы прочитать полное описание."}
	var rows [][]models.InlineKeyboardButton
	start := page * featurePageSize
	end := min(start+featurePageSize, len(view.Options))
	for _, option := range view.Options[start:end] {
		mark := ""
		if option.IdeaID == view.SelectedID {
			mark = "✅ "
		}
		label := truncateFeatureButton(mark + option.Title)
		rows = append(rows, []models.InlineKeyboardButton{{Text: label, CallbackData: fmt.Sprintf("fv:d:%s:%d:%d", token, option.IdeaID, page)}})
	}
	if pages > 1 {
		nav := []models.InlineKeyboardButton{}
		if page > 0 {
			nav = append(nav, models.InlineKeyboardButton{Text: "←", CallbackData: fmt.Sprintf("fv:p:%s:%d", token, page-1)})
		}
		nav = append(nav, models.InlineKeyboardButton{Text: fmt.Sprintf("%d/%d", page+1, pages), CallbackData: fmt.Sprintf("fv:p:%s:%d", token, page)})
		if page+1 < pages {
			nav = append(nav, models.InlineKeyboardButton{Text: "→", CallbackData: fmt.Sprintf("fv:p:%s:%d", token, page+1)})
		}
		rows = append(rows, nav)
	}
	return strings.Join(lines, "\n"), &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func featureDetailView(view featurevote.View, token string, option featurevote.Option, page int) (string, *models.InlineKeyboardMarkup) {
	selected := ""
	if view.SelectedID == option.IdeaID {
		selected = "\n\n✅ <b>Ваш текущий выбор</b>"
	}
	text := fmt.Sprintf("💡 <b>%s</b>\n\n%s%s", html.EscapeString(option.Title), html.EscapeString(option.Text), selected)
	rows := [][]models.InlineKeyboardButton{}
	if view.Round.State == featurevote.RoundOpen {
		label := "Выбрать эту идею"
		if view.SelectedID == option.IdeaID {
			label = "✅ Идея выбрана"
		}
		rows = append(rows, []models.InlineKeyboardButton{{Text: label, CallbackData: fmt.Sprintf("fv:v:%s:%d:%d", token, option.IdeaID, page)}})
	}
	rows = append(rows, []models.InlineKeyboardButton{{Text: "← Ко всем идеям", CallbackData: fmt.Sprintf("fv:p:%s:%d", token, page)}})
	return text, &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func featureOption(options []featurevote.Option, ideaID int64) featurevote.Option {
	for _, option := range options {
		if option.IdeaID == ideaID {
			return option
		}
	}
	return featurevote.Option{}
}

func featureLeaders(options []featurevote.Option) (int, int) {
	maxVotes, leaders := 0, 0
	for _, option := range options {
		if option.Votes > maxVotes {
			maxVotes, leaders = option.Votes, 1
		} else if option.Votes == maxVotes && maxVotes > 0 {
			leaders++
		}
	}
	return leaders, maxVotes
}

func (h *Handler) featureMember(ctx context.Context, chatID, userID int64) bool {
	member, err := h.API.GetChatMember(ctx, &bot.GetChatMemberParams{ChatID: chatID, UserID: userID})
	if err != nil || member == nil {
		h.Log.Warn("feature vote membership unavailable", "chat_id", chatID)
		return false
	}
	return member.Type != models.ChatMemberTypeLeft && member.Type != models.ChatMemberTypeBanned
}

func truncateFeatureButton(value string) string {
	runes := []rune(value)
	if len(runes) <= 56 {
		return value
	}
	return strings.TrimSpace(string(runes[:55])) + "…"
}

func featureIdeasText(heading string, ideas []featurevote.Idea) string {
	lines := []string{heading}
	for _, idea := range ideas {
		title := idea.Title
		if title == "" {
			title = featurevote.FallbackTitle(idea.Text)
		}
		lines = append(lines, fmt.Sprintf("#%d · %s", idea.ID, title))
	}
	if len(ideas) == 0 {
		lines = append(lines, "Список пуст.")
	}
	return strings.Join(lines, "\n")
}

func featureSettingsText(view featurevote.SettingsView) string {
	lines := []string{"💡 Голосования за функции", fmt.Sprintf("Активных идей: %d", view.Active)}
	if !view.Configured {
		lines = append(lines, "Расписание выключено")
	} else {
		state := "выключено"
		if view.Schedule.Enabled {
			state = "включено"
		}
		lines = append(lines, fmt.Sprintf("Расписание: %s %s · %s · %s", featurevote.WeekdayName(view.Schedule.Weekday), view.Schedule.Clock, view.Schedule.Zone, state))
	}
	for _, round := range view.Latest {
		line := fmt.Sprintf("Раунд #%d · %s", round.ID, round.State)
		if round.ErrorCode != "" {
			line += " · " + round.ErrorCode
		}
		lines = append(lines, line)
		if round.State == featurevote.RoundUnknown || round.State == featurevote.RoundFailed {
			lines = append(lines, fmt.Sprintf("/idea_resolve %d sent|retry|cancel", round.ID))
		}
	}
	return strings.Join(lines, "\n")
}

func (h *Handler) featureCallback(ctx context.Context, query *models.CallbackQuery, message *models.Message) bool {
	if h.Features == nil || message.Chat.Type != models.ChatTypePrivate || query.From.IsBot {
		return false
	}
	parts := strings.Split(query.Data, ":")
	if len(parts) < 4 || parts[0] != "fv" {
		return false
	}
	action, token := parts[1], parts[2]
	page, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil {
		return true
	}
	switch action {
	case "p":
		_, _ = h.API.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: query.ID})
		h.sendFeatureList(ctx, message.Chat.ID, query.From.ID, token, page, message.ID)
	case "d":
		h.featureDetailCallback(ctx, query, message, parts, token, page)
	case "v":
		h.featureVoteCallback(ctx, query, message, parts, token, page)
	}
	return true
}

func (h *Handler) featureDetailCallback(ctx context.Context, query *models.CallbackQuery, message *models.Message, parts []string, token string, page int) {
	if len(parts) != 5 {
		return
	}
	ideaID, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil {
		return
	}
	_, _ = h.API.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: query.ID})
	h.sendFeatureDetail(ctx, message.Chat.ID, query.From.ID, token, ideaID, page, message.ID)
}

func (h *Handler) featureVoteCallback(ctx context.Context, query *models.CallbackQuery, message *models.Message, parts []string, token string, page int) {
	if len(parts) != 5 {
		return
	}
	ideaID, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil {
		return
	}
	view, err := h.Features.View(ctx, token, query.From.ID)
	if err != nil || !h.featureMember(ctx, view.Round.ChatID, query.From.ID) {
		_, _ = h.API.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: query.ID, Text: "Нет доступа к голосованию"})
		return
	}
	if err = h.Features.Vote(ctx, token, query.From.ID, ideaID); err != nil {
		_, _ = h.API.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: query.ID, Text: "Голосование уже закрыто"})
		return
	}
	_, _ = h.API.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: query.ID, Text: "Голос сохранён"})
	h.sendFeatureDetail(ctx, message.Chat.ID, query.From.ID, token, ideaID, page, message.ID)
}

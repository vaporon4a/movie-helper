package telegram

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/vaporon4a/movie-helper/internal/daily"
)

func (h *Handler) handleDailyCommand(ctx context.Context, update *models.Update, command, args string) (bool, error) {
	message := update.Message
	chatID := message.Chat.ID
	now := h.Now()
	switch command {
	case "/preview":
		return true, h.preview(ctx, chatID, args)
	case "/settings":
		return true, h.settings(ctx, chatID, now)
	case "/moderation":
		if args != "on" && args != "off" {
			h.reply(ctx, chatID, "Формат: /moderation on — ручное одобрение; /moderation off — автоматический отбор AI.")
			return true, errResponseSent
		}
		err := h.Daily.SetModeration(ctx, update.ID, chatID, args == "on", now)
		if err == nil {
			h.reply(ctx, chatID, moderationText(args == "on")+" Расписание сохранено; уже отправляемый пост может завершиться.")
			return true, errResponseSent
		}
		return true, err
	case "/timezone":
		return true, h.Daily.SetZone(ctx, update.ID, chatID, args, now)
	case "/schedule":
		fields := strings.Fields(args)
		if len(fields) != 2 {
			h.reply(ctx, chatID, "Пример: /schedule meme 09:00. Сначала задайте /timezone.")
			return true, errResponseSent
		}
		return true, h.Daily.SetSchedule(ctx, update.ID, chatID, fields[0], fields[1], true, now)
	case "/pause":
		return true, h.Daily.SetSchedule(ctx, update.ID, chatID, args, "09:00", false, now)
	case "/resume":
		return true, h.Daily.Resume(ctx, update.ID, chatID)
	case "/suggest_fact":
		return true, h.suggestFact(ctx, update, args, now)
	case "/suggest_meme":
		return true, h.suggestMeme(ctx, update, now)
	case "/queue":
		return true, h.queue(ctx, chatID, args)
	case "/review":
		return true, h.review(ctx, chatID, args)
	case "/approve", "/reject":
		itemID, err := strconv.ParseInt(args, 10, 64)
		if err != nil {
			return true, err
		}
		return true, h.Daily.Moderate(ctx, update.ID, chatID, itemID, message.From.ID, command == "/approve", now)
	case "/resolve":
		return true, h.resolveDaily(ctx, update.ID, chatID, args)
	default:
		return false, nil
	}
}

func (h *Handler) preview(ctx context.Context, chatID int64, kind string) error {
	if !daily.ValidKind(kind) {
		h.reply(ctx, chatID, "Формат: /preview meme или /preview fact.")
		return errResponseSent
	}
	fetchCtx, cancel := context.WithTimeout(ctx, daily.FetchTimeout)
	items, err := h.Daily.Candidates(fetchCtx, kind, chatID)
	cancel()
	if err != nil {
		message, reason := previewError(err)
		h.Log.Warn("preview source unavailable", "chat_id", chatID, "kind", kind, "reason", reason)
		h.reply(ctx, chatID, message)
		return errResponseSent
	}
	if len(items) == 0 {
		h.reply(ctx, chatID, "Подходящего материала нет. AI мог отклонить кандидатов; для автоматического подбора нужен ключ Gemini или Groq.")
		return errResponseSent
	}
	item := items[0]
	item.ChatID, item.Kind = chatID, kind
	if err = item.Validate(); err != nil {
		h.reply(ctx, chatID, "Подготовленный материал не прошёл проверку формата.")
		return errResponseSent
	}
	item.Text = "Предпросмотр · " + item.Text
	if _, err = (Sender{API: h.API}).Send(ctx, chatID, item); err != nil {
		h.Log.Warn("preview delivery not confirmed", "chat_id", chatID)
		h.reply(ctx, chatID, "Доставка предпросмотра не подтверждена. Проверьте чат перед повторной командой.")
	}
	return errResponseSent
}

func (h *Handler) settings(ctx context.Context, chatID int64, now time.Time) error {
	schedules, err := h.Daily.Schedules(ctx, chatID)
	if err != nil {
		return err
	}
	issues, err := h.Daily.Issues(ctx, chatID)
	if err != nil {
		return err
	}
	h.reply(ctx, chatID, settingsText(schedules, issues, now))
	return errResponseSent
}

func (h *Handler) suggestFact(ctx context.Context, update *models.Update, args string, now time.Time) error {
	text, source, ok := strings.Cut(args, "|")
	if !ok {
		h.reply(ctx, update.Message.Chat.ID, "Формат: /suggest_fact Текст факта | https://источник")
		return errResponseSent
	}
	text = strings.TrimSpace(text)
	source = strings.TrimSpace(source)
	item := daily.Item{
		ChatID: update.Message.Chat.ID, AuthorID: update.Message.From.ID, Kind: daily.Fact,
		Text: text, Source: source,
		Key: fmt.Sprintf("fact:%x", sha256.Sum256([]byte(strings.Join(strings.Fields(text), " ")+"|"+source))),
	}
	if err := item.Validate(); err != nil {
		h.reply(ctx, item.ChatID, "Нужны текст до 1500 символов и HTTPS-ссылка до 600 символов.")
		return errResponseSent
	}
	itemID, err := h.Daily.Add(ctx, update.ID, item, now)
	if err == nil {
		h.reply(ctx, item.ChatID, fmt.Sprintf("Факт #%d сохранён в ручную очередь. Администратор: /review %d. Очередь публикуется при /moderation on.", itemID, itemID))
		return errResponseSent
	}
	return err
}

func (h *Handler) suggestMeme(ctx context.Context, update *models.Update, now time.Time) error {
	message := update.Message
	photo := message.ReplyToMessage
	if photo == nil || len(photo.Photo) == 0 || photo.MediaGroupID != "" {
		h.reply(ctx, message.Chat.ID, "Ответьте командой на одно фото, не альбом.")
		return errResponseSent
	}
	image := photo.Photo[len(photo.Photo)-1]
	caption := []rune(photo.Caption)
	if len(caption) > 200 {
		caption = caption[:200]
	}
	item := daily.Item{ChatID: message.Chat.ID, AuthorID: message.From.ID, Kind: daily.Meme, Image: image.FileID, Text: string(caption), Key: "telegram:" + image.FileUniqueID}
	itemID, err := h.Daily.Add(ctx, update.ID, item, now)
	if err == nil {
		h.reply(ctx, item.ChatID, fmt.Sprintf("Мем #%d сохранён в ручную очередь. Администратор: /review %d. Очередь публикуется при /moderation on.", itemID, itemID))
		return errResponseSent
	}
	return err
}

func (h *Handler) queue(ctx context.Context, chatID int64, args string) error {
	var after int64
	var err error
	if args != "" {
		after, err = strconv.ParseInt(args, 10, 64)
		if err != nil {
			return err
		}
	}
	items, err := h.Daily.Queue(ctx, chatID, after)
	if err != nil {
		return err
	}
	h.reply(ctx, chatID, queueText(items))
	return errResponseSent
}

func (h *Handler) review(ctx context.Context, chatID int64, args string) error {
	itemID, err := strconv.ParseInt(args, 10, 64)
	if err != nil {
		return err
	}
	item, err := h.Daily.Item(ctx, chatID, itemID)
	if err != nil {
		return err
	}
	if item.State != "pending" && item.State != "approved" {
		return daily.ErrConflict
	}
	keyboard := &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{
		{Text: "Одобрить", CallbackData: fmt.Sprintf("approve:%d", itemID)},
		{Text: "Убрать", CallbackData: fmt.Sprintf("reject:%d", itemID)},
	}}}
	if item.Kind == daily.Meme {
		_, err = h.API.SendPhoto(ctx, &bot.SendPhotoParams{ChatID: chatID, Photo: &models.InputFileString{Data: item.Image}, Caption: fmt.Sprintf("Предпросмотр #%d\n%s", itemID, item.Text), ReplyMarkup: keyboard})
	} else {
		_, err = h.API.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: fmt.Sprintf("Предпросмотр #%d\n%s\nИсточник: %s", itemID, item.Text, item.Source), ReplyMarkup: keyboard})
	}
	if err == nil {
		return errResponseSent
	}
	return err
}

func (h *Handler) resolveDaily(ctx context.Context, operationID, chatID int64, args string) error {
	fields := strings.Fields(args)
	if len(fields) != 2 || (fields[1] != "sent" && fields[1] != "requeue") {
		h.reply(ctx, chatID, "Проверьте, появилось ли сообщение. /resolve ID sent — уже отправлено; /resolve ID requeue — вернуть в очередь следующего дня (возможен повтор).")
		return errResponseSent
	}
	deliveryID, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return err
	}
	return h.Daily.Resolve(ctx, operationID, chatID, deliveryID, fields[1] == "sent")
}

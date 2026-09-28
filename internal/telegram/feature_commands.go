package telegram

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/vaporon4a/movie-helper/internal/featurevote"
)

const ideaPrompt = "💡 Опишите новую функцию одним сообщением. Отправьте от 20 до 1500 символов."

func (h *Handler) handleFeatureCommand(ctx context.Context, update *models.Update, command, args string) (bool, error) {
	if !isFeatureCommand(command) {
		return false, nil
	}
	chatID := update.Message.Chat.ID
	if h.Features == nil {
		h.reply(ctx, chatID, "Сбор идей временно недоступен.")
		return true, errResponseSent
	}
	switch command {
	case "/idea", "/feature":
		if strings.TrimSpace(args) == "" {
			_, err := h.API.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: ideaPrompt, ReplyMarkup: &models.ForceReply{ForceReply: true, Selective: true, InputFieldPlaceholder: "Опишите идею"}})
			return true, responseError(err)
		}
		return true, h.addIdea(ctx, update.ID, chatID, update.Message.From.ID, args)
	case "/my_ideas":
		ideas, err := h.Features.Mine(ctx, chatID, update.Message.From.ID)
		if err == nil {
			h.reply(ctx, chatID, featureIdeasText("💡 Ваши активные идеи", ideas))
			return true, errResponseSent
		}
		return true, err
	case "/idea_cancel":
		id, err := strconv.ParseInt(args, 10, 64)
		if err != nil {
			h.reply(ctx, chatID, "Формат: /idea_cancel ID")
			return true, errResponseSent
		}
		err = h.Features.ChangeState(ctx, update.ID, chatID, id, update.Message.From.ID, featurevote.IdeaRemoved, false)
		if err == nil {
			h.reply(ctx, chatID, fmt.Sprintf("Идея #%d отозвана.", id))
			return true, errResponseSent
		}
		return true, err
	case "/idea_schedule":
		return true, h.setFeatureSchedule(ctx, update.ID, chatID, args)
	case "/idea_pause":
		return true, h.Features.Pause(ctx, update.ID, chatID)
	case "/idea_round":
		return true, h.startFeatureRound(ctx, update.ID, chatID, args)
	case "/idea_settings":
		settings, err := h.Features.Settings(ctx, chatID)
		if err == nil {
			h.reply(ctx, chatID, featureSettingsText(settings))
			return true, errResponseSent
		}
		return true, err
	case "/idea_backlog":
		ideas, err := h.Features.Backlog(ctx, chatID)
		if err == nil {
			h.reply(ctx, chatID, featureIdeasText("🧰 Бэклог разработки", ideas))
			return true, errResponseSent
		}
		return true, err
	case "/idea_remove", "/idea_done", "/idea_restore":
		return true, h.changeFeatureState(ctx, update, command, args)
	case "/idea_resolve":
		return true, h.resolveFeatureRound(ctx, update.ID, chatID, args)
	default:
		return false, nil
	}
}

func isFeatureCommand(command string) bool {
	switch command {
	case "/idea", "/feature", "/my_ideas", "/idea_cancel", "/idea_schedule", "/idea_pause", "/idea_round", "/idea_settings", "/idea_backlog", "/idea_remove", "/idea_done", "/idea_restore", "/idea_resolve":
		return true
	default:
		return false
	}
}

func isIdeaReply(message *models.Message) bool {
	return message != nil && message.ReplyToMessage != nil && message.ReplyToMessage.Text == ideaPrompt
}

func (h *Handler) handleIdeaReply(ctx context.Context, update *models.Update) bool {
	message := update.Message
	if !isIdeaReply(message) || h.Features == nil || !h.Allowed[message.Chat.ID] {
		return false
	}
	err := h.addIdea(ctx, update.ID, message.Chat.ID, message.From.ID, message.Text)
	if !errorsIsResponseSent(err) {
		h.finishCommand(ctx, message.Chat.ID, "/idea", err)
	}
	return true
}

func (h *Handler) addIdea(ctx context.Context, operationID, chatID, authorID int64, text string) error {
	idea, err := h.Features.Add(ctx, operationID, chatID, authorID, text)
	if err != nil {
		return err
	}
	h.reply(ctx, chatID, fmt.Sprintf("💡 Идея #%d сохранена и будет участвовать в следующем голосовании.", idea.ID))
	return errResponseSent
}

func responseError(err error) error {
	if err == nil {
		return errResponseSent
	}
	return err
}

func errorsIsResponseSent(err error) bool { return err == errResponseSent }

func (h *Handler) setFeatureSchedule(ctx context.Context, operationID, chatID int64, args string) error {
	fields := strings.Fields(args)
	if len(fields) != 2 {
		h.reply(ctx, chatID, "Формат: /idea_schedule sun 18:00. Сначала задайте /timezone.")
		return errResponseSent
	}
	weekday, ok := featurevote.ParseWeekday(fields[0])
	if !ok {
		h.reply(ctx, chatID, "День недели: mon..sun или пн..вс.")
		return errResponseSent
	}
	return h.Features.SetSchedule(ctx, operationID, chatID, weekday, fields[1])
}

func (h *Handler) startFeatureRound(ctx context.Context, operationID, chatID int64, args string) error {
	duration := featurevote.DefaultRoundDuration
	if args != "" {
		parsed, err := time.ParseDuration(args)
		if err != nil {
			h.reply(ctx, chatID, "Формат: /idea_round 10m. Допустимо от 5m до 24h.")
			return errResponseSent
		}
		duration = parsed
	}
	id, err := h.Features.Start(ctx, operationID, chatID, duration)
	if err == nil {
		h.reply(ctx, chatID, fmt.Sprintf("💡 Голосование за функции #%d запланировано на %s.", id, duration))
		return errResponseSent
	}
	return err
}

func (h *Handler) changeFeatureState(ctx context.Context, update *models.Update, command, args string) error {
	id, err := strconv.ParseInt(args, 10, 64)
	if err != nil {
		h.reply(ctx, update.Message.Chat.ID, "Формат: "+command+" ID")
		return errResponseSent
	}
	state := featurevote.IdeaRemoved
	switch command {
	case "/idea_done":
		state = featurevote.IdeaImplemented
	case "/idea_restore":
		state = featurevote.IdeaActive
	}
	if err = h.Features.ChangeState(ctx, update.ID, update.Message.Chat.ID, id, update.Message.From.ID, state, true); err != nil {
		return err
	}
	message := "исключена из голосований"
	switch state {
	case featurevote.IdeaImplemented:
		message = "отмечена реализованной"
	case featurevote.IdeaActive:
		message = "возвращена в голосования"
	}
	h.reply(ctx, update.Message.Chat.ID, fmt.Sprintf("Идея #%d %s.", id, message))
	return errResponseSent
}

func (h *Handler) resolveFeatureRound(ctx context.Context, operationID, chatID int64, args string) error {
	fields := strings.Fields(args)
	if len(fields) != 2 {
		h.reply(ctx, chatID, "Формат: /idea_resolve ID sent|retry|cancel")
		return errResponseSent
	}
	id, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return err
	}
	action := featurevote.ResolveAction(fields[1])
	if err = h.Features.Resolve(ctx, operationID, chatID, id, action); err != nil {
		return err
	}
	h.reply(ctx, chatID, fmt.Sprintf("Состояние голосования #%d обновлено: %s.", id, action))
	return errResponseSent
}

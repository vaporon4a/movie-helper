package telegram

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/vaporon4a/movie-helper/internal/daily"
	"github.com/vaporon4a/movie-helper/internal/movieclub"
)

var errMissingDependency = errors.New("telegram handler dependencies are required")
var errResponseSent = errors.New("telegram response already sent")

func Command(text, username string) (string, string) {
	parts := strings.SplitN(strings.TrimSpace(text), " ", 2)
	if len(parts) == 0 || !strings.HasPrefix(parts[0], "/") {
		return "", ""
	}
	command, target, qualified := strings.Cut(parts[0], "@")
	if qualified && !strings.EqualFold(target, username) {
		return "", ""
	}
	args := ""
	if len(parts) == 2 {
		args = strings.TrimSpace(parts[1])
	}
	return strings.ToLower(command), args
}

func (h *Handler) Handle(ctx context.Context, _ *bot.Bot, update *models.Update) {
	timeout := 25 * time.Second
	if update.Message != nil {
		if command, _ := Command(update.Message.Text, h.Username); command == "/preview" {
			timeout = daily.PreviewTimeout
		}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	switch {
	case update.MyChatMember != nil:
		h.handleMembership(ctx, update.MyChatMember)
	case update.Poll != nil:
		h.handlePoll(ctx, update.Poll)
	case update.CallbackQuery != nil:
		h.callback(ctx, update)
	case update.Message != nil:
		h.handleMessage(ctx, update)
	}
}

func (h *Handler) handleMembership(ctx context.Context, member *models.ChatMemberUpdated) {
	left := member.NewChatMember.Type == models.ChatMemberTypeLeft || member.NewChatMember.Type == models.ChatMemberTypeBanned
	if h.Allowed[member.Chat.ID] && left {
		if err := h.Daily.Suspend(ctx, member.Chat.ID); err != nil {
			h.Log.Error("could not suspend chat", "chat_id", member.Chat.ID)
		}
	}
}

func (h *Handler) handlePoll(ctx context.Context, poll *models.Poll) {
	if h.MovieClub == nil || !poll.IsClosed {
		return
	}
	votes := make([]int, len(poll.Options))
	for i, option := range poll.Options {
		votes[i] = option.VoterCount
	}
	if err := h.MovieClub.PollClosed(ctx, poll.ID, votes); err != nil && !errors.Is(err, movieclub.ErrConflict) {
		h.Log.Warn("movie poll update not applied")
	}
}

func (h *Handler) handleMessage(ctx context.Context, update *models.Update) {
	message := update.Message
	command, args := Command(message.Text, h.Username)
	if h.handleBasicMessage(ctx, message, command) {
		return
	}
	if message.From == nil || message.From.IsBot || message.SenderChat != nil {
		h.reply(ctx, message.Chat.ID, "Отправьте команду от своего имени.")
		return
	}
	if requiresAdmin(command) && !h.admin(ctx, message.Chat.ID, message.From.ID) {
		h.reply(ctx, message.Chat.ID, "Нужны подтверждённые права администратора чата.")
		return
	}

	handled, err := h.handleMovieCommand(ctx, update, command, args)
	if !handled {
		handled, err = h.handleDailyCommand(ctx, update, command, args)
	}
	if !handled {
		h.reply(ctx, message.Chat.ID, "Команды: /help")
		return
	}
	h.finishCommand(ctx, message.Chat.ID, command, err)
}

func (h *Handler) handleBasicMessage(ctx context.Context, message *models.Message, command string) bool {
	if command == "/id" {
		h.reply(ctx, message.Chat.ID, fmt.Sprintf("ID чата: %d", message.Chat.ID))
		return true
	}
	if message.Chat.Type == models.ChatTypePrivate {
		h.handlePrivate(ctx, message.Chat.ID, command)
		return true
	}
	if !h.Allowed[message.Chat.ID] || (message.Chat.Type != models.ChatTypeGroup && message.Chat.Type != models.ChatTypeSupergroup) {
		return true
	}
	if message.MigrateToChatID != 0 || message.MigrateFromChatID != 0 {
		h.handleMigration(ctx, message)
		return true
	}
	if command == "" {
		return true
	}
	if err := h.Daily.EnsureChat(ctx, message.Chat.ID); err != nil {
		h.problem(ctx, message.Chat.ID)
		return true
	}
	if h.handleReference(ctx, message.Chat.ID, command) {
		return true
	}
	return false
}

func (h *Handler) handlePrivate(ctx context.Context, chatID int64, command string) {
	switch command {
	case "/start", "/help":
		h.reply(ctx, chatID, help)
	case "/about":
		h.reply(ctx, chatID, about)
	case "/help_admin":
		h.reply(ctx, chatID, helpAdmin)
	}
}

func (h *Handler) handleMigration(ctx context.Context, message *models.Message) {
	oldChatID := message.Chat.ID
	if message.MigrateFromChatID != 0 {
		oldChatID = message.MigrateFromChatID
	}
	if err := h.Daily.Suspend(ctx, oldChatID); err != nil {
		h.Log.Error("could not suspend migrated chat", "chat_id", oldChatID)
	}
	h.reply(ctx, message.Chat.ID, "Чат преобразован: рассылка приостановлена. Обновите ALLOWED_CHAT_IDS на сервере и настройте расписание в новом чате. Старая история остаётся в базе.")
}

func (h *Handler) handleReference(ctx context.Context, chatID int64, command string) bool {
	switch command {
	case "/start", "/help":
		h.reply(ctx, chatID, help)
	case "/help_admin":
		h.reply(ctx, chatID, helpAdmin)
	case "/about":
		h.reply(ctx, chatID, about)
	default:
		return false
	}
	return true
}

func requiresAdmin(command string) bool {
	return command != "/suggest_meme" && command != "/suggest_fact" && command != "/settings"
}

func (h *Handler) finishCommand(ctx context.Context, chatID int64, command string, err error) {
	if errors.Is(err, errResponseSent) {
		return
	}
	if err == nil {
		h.reply(ctx, chatID, "Готово. Изменения расписания действуют со следующего будущего времени публикации.")
		return
	}
	if errors.Is(err, daily.ErrDuplicate) || errors.Is(err, movieclub.ErrDuplicate) {
		h.reply(ctx, chatID, "Уже обработано или такой материал уже есть.")
		return
	}
	if errors.Is(err, movieclub.ErrActiveRound) {
		h.reply(ctx, chatID, "В этом чате уже идёт киноопрос или публикуется его результат.")
		return
	}
	h.Log.Warn("command not applied", "command", command, "chat_id", chatID)
	h.reply(ctx, chatID, "Не удалось применить команду. Проверьте формат, часовой пояс, ID и состояние материала. /help")
}

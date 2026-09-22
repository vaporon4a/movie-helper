// Package telegram translates Telegram commands to chat-scoped application actions.
package telegram

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/vaporon4a/movie-helper/internal/daily"
	"github.com/vaporon4a/movie-helper/internal/storage"
)

type API interface {
	SendMessage(context.Context, *bot.SendMessageParams) (*models.Message, error)
	SendPhoto(context.Context, *bot.SendPhotoParams) (*models.Message, error)
	GetChatAdministrators(context.Context, *bot.GetChatAdministratorsParams) ([]models.ChatMember, error)
	AnswerCallbackQuery(context.Context, *bot.AnswerCallbackQueryParams) (bool, error)
}
type Handler struct {
	API      API
	Store    *storage.Store
	Allowed  map[int64]bool
	Username string
	Log      *slog.Logger
	Now      func() time.Time
}

const help = `Киноклуб: мем дня и факты о кино.
/id — ID текущего чата
/settings — расписание и ошибки публикаций
/timezone Europe/Moscow — часовой пояс
/schedule meme 09:00 — включить мем дня
/schedule fact 12:00 — включить факт дня
/pause meme или /pause fact — выключить рубрику
/suggest_meme — ответьте этой командой на одно фото
/suggest_fact Текст факта | https://источник — предложить факт
/queue — очередь (следующая страница: /queue последний_ID)
/review ID — показать материал и кнопки одобрения
/approve ID или /reject ID — одобрить или убрать материал
/resolve ID sent — подтвердить неопределённую доставку
/resolve ID requeue — вернуть её материал на следующий день
/resume — восстановить работу после потери доступа

Настройка, очередь и одобрение доступны администраторам.
Мемы: очередь → Reddit с отбором Gemini, если он подключён. Факты: очередь → Wikipedia с обработкой Gemini. Рассылка изначально выключена.`

func Command(text, username string) (string, string) {
	parts := strings.SplitN(strings.TrimSpace(text), " ", 2)
	if len(parts) == 0 || !strings.HasPrefix(parts[0], "/") {
		return "", ""
	}
	cmd, target, qualified := strings.Cut(parts[0], "@")
	if qualified && !strings.EqualFold(target, username) {
		return "", ""
	}
	args := ""
	if len(parts) == 2 {
		args = strings.TrimSpace(parts[1])
	}
	return strings.ToLower(cmd), args
}
func (h *Handler) Handle(ctx context.Context, _ *bot.Bot, u *models.Update) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	if u.MyChatMember != nil {
		m := u.MyChatMember
		if h.Allowed[m.Chat.ID] && (m.NewChatMember.Type == models.ChatMemberTypeLeft || m.NewChatMember.Type == models.ChatMemberTypeBanned) {
			if err := h.Store.Suspend(ctx, m.Chat.ID); err != nil {
				h.Log.Error("could not suspend chat")
			}
		}
		return
	}
	if u.CallbackQuery != nil {
		h.callback(ctx, u)
		return
	}
	m := u.Message
	if m == nil {
		return
	}
	cmd, args := Command(m.Text, h.Username)
	if cmd == "/id" {
		h.reply(ctx, m.Chat.ID, fmt.Sprintf("ID чата: %d", m.Chat.ID))
		return
	}
	if m.Chat.Type == models.ChatTypePrivate {
		if cmd == "/start" || cmd == "/help" {
			h.reply(ctx, m.Chat.ID, help)
		}
		return
	}
	if !h.Allowed[m.Chat.ID] || (m.Chat.Type != models.ChatTypeGroup && m.Chat.Type != models.ChatTypeSupergroup) {
		return
	}
	if m.MigrateToChatID != 0 || m.MigrateFromChatID != 0 {
		old := m.Chat.ID
		if m.MigrateFromChatID != 0 {
			old = m.MigrateFromChatID
		}
		if err := h.Store.Suspend(ctx, old); err != nil {
			h.Log.Error("could not suspend migrated chat")
		}
		h.reply(ctx, m.Chat.ID, "Чат преобразован: рассылка приостановлена. Обновите ALLOWED_CHAT_IDS на сервере и настройте расписание в новом чате. Старая история остаётся в базе.")
		return
	}
	if cmd == "" {
		return
	}
	if err := h.Store.EnsureChat(ctx, m.Chat.ID); err != nil {
		h.problem(ctx, m.Chat.ID)
		return
	}
	if cmd == "/start" || cmd == "/help" {
		h.reply(ctx, m.Chat.ID, help)
		return
	}
	if m.From == nil || m.From.IsBot || m.SenderChat != nil {
		h.reply(ctx, m.Chat.ID, "Отправьте команду от своего имени.")
		return
	}
	if cmd != "/suggest_meme" && cmd != "/suggest_fact" && cmd != "/settings" && !h.admin(ctx, m.Chat.ID, m.From.ID) {
		h.reply(ctx, m.Chat.ID, "Нужны подтверждённые права администратора чата.")
		return
	}
	var err error
	now := h.Now()
	chat := m.Chat.ID
	switch cmd {
	case "/settings":
		sc, e := h.Store.Schedules(ctx, chat)
		if e != nil {
			err = e
			break
		}
		lines := []string{"Расписание (по времени чата):"}
		for _, s := range sc {
			zone := s.Zone
			if zone == "" {
				zone = "не выбран — /timezone"
			}
			on := "выключено"
			if s.Enabled {
				on = "включено"
			}
			lines = append(lines, fmt.Sprintf("%s: %s, %s, %s", s.Kind, s.Clock, zone, on))
		}
		issues, e := h.Store.Issues(ctx, chat)
		if e != nil {
			err = e
			break
		}
		for _, d := range issues {
			lines = append(lines, fmt.Sprintf("Доставка #%d: %s, %s, %s", d.ID, d.Kind, d.Date, d.State))
		}
		lines = append(lines, "Мемы: очередь → Reddit. Факты: очередь → Wikipedia, если подключён Gemini.", "unknown: проверьте чат и выполните /resolve ID sent или /resolve ID requeue.")
		h.reply(ctx, chat, strings.Join(lines, "\n"))
		return
	case "/timezone":
		err = h.Store.SetZone(ctx, u.ID, chat, args, now)
	case "/schedule":
		a := strings.Fields(args)
		if len(a) != 2 {
			h.reply(ctx, chat, "Пример: /schedule meme 09:00. Сначала задайте /timezone.")
			return
		}
		err = h.Store.SetSchedule(ctx, u.ID, chat, a[0], a[1], true, now)
	case "/pause":
		err = h.Store.SetSchedule(ctx, u.ID, chat, args, "09:00", false, now)
	case "/resume":
		err = h.Store.Resume(ctx, u.ID, chat)
	case "/suggest_fact":
		text, source, ok := strings.Cut(args, "|")
		if !ok {
			h.reply(ctx, chat, "Формат: /suggest_fact Текст факта | https://источник")
			return
		}
		text = strings.TrimSpace(text)
		source = strings.TrimSpace(source)
		i := daily.Item{ChatID: chat, AuthorID: m.From.ID, Kind: daily.Fact, Text: text, Source: source, Key: fmt.Sprintf("fact:%x", sha256.Sum256([]byte(strings.Join(strings.Fields(text), " ")+"|"+source)))}
		if e := i.Validate(); e != nil {
			h.reply(ctx, chat, "Нужны текст до 1500 символов и HTTPS-ссылка до 600 символов.")
			return
		}
		var id int64
		id, err = h.Store.Add(ctx, u.ID, i, now)
		if err == nil {
			h.reply(ctx, chat, fmt.Sprintf("Факт #%d предложен. Администратор: /review %d", id, id))
			return
		}
	case "/suggest_meme":
		photo := m.ReplyToMessage
		if photo == nil || len(photo.Photo) == 0 || photo.MediaGroupID != "" {
			h.reply(ctx, chat, "Ответьте командой на одно фото, не альбом.")
			return
		}
		p := photo.Photo[len(photo.Photo)-1]
		caption := []rune(photo.Caption)
		if len(caption) > 200 {
			caption = caption[:200]
		}
		i := daily.Item{ChatID: chat, AuthorID: m.From.ID, Kind: daily.Meme, Image: p.FileID, Text: string(caption), Key: "telegram:" + p.FileUniqueID}
		var id int64
		id, err = h.Store.Add(ctx, u.ID, i, now)
		if err == nil {
			h.reply(ctx, chat, fmt.Sprintf("Мем #%d предложен. Администратор: /review %d", id, id))
			return
		}
	case "/queue":
		var after int64
		if args != "" {
			after, err = strconv.ParseInt(args, 10, 64)
			if err != nil {
				break
			}
		}
		items, e := h.Store.Queue(ctx, chat, after)
		if e != nil {
			err = e
			break
		}
		lines := []string{"Очередь материалов:"}
		for _, i := range items {
			title := []rune(i.Text)
			if len(title) > 70 {
				title = title[:70]
			}
			lines = append(lines, fmt.Sprintf("#%d %s [%s] %s", i.ID, i.Kind, i.State, string(title)))
		}
		if len(items) == 0 {
			lines = append(lines, "Очередь пуста. Факты можно добавить через /suggest_fact; мемы также поступают автоматически.")
		}
		if len(items) == 10 {
			lines = append(lines, fmt.Sprintf("Дальше: /queue %d", items[9].ID))
		}
		h.reply(ctx, chat, strings.Join(lines, "\n"))
		return
	case "/review":
		id, e := strconv.ParseInt(args, 10, 64)
		if e != nil {
			err = e
			break
		}
		i, e := h.Store.Item(ctx, chat, id)
		if e != nil {
			err = e
			break
		}
		if i.State != "pending" && i.State != "approved" {
			err = daily.ErrConflict
			break
		}
		keyboard := &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{{Text: "Одобрить", CallbackData: fmt.Sprintf("approve:%d", id)}, {Text: "Убрать", CallbackData: fmt.Sprintf("reject:%d", id)}}}}
		if i.Kind == daily.Meme {
			_, err = h.API.SendPhoto(ctx, &bot.SendPhotoParams{ChatID: chat, Photo: &models.InputFileString{Data: i.Image}, Caption: fmt.Sprintf("Предпросмотр #%d\n%s", id, i.Text), ReplyMarkup: keyboard})
		} else {
			_, err = h.API.SendMessage(ctx, &bot.SendMessageParams{ChatID: chat, Text: fmt.Sprintf("Предпросмотр #%d\n%s\nИсточник: %s", id, i.Text, i.Source), ReplyMarkup: keyboard})
		}
		if err == nil {
			return
		}
	case "/approve", "/reject":
		id, e := strconv.ParseInt(args, 10, 64)
		if e != nil {
			err = e
			break
		}
		err = h.Store.Moderate(ctx, u.ID, chat, id, m.From.ID, cmd == "/approve", now)
	case "/resolve":
		a := strings.Fields(args)
		if len(a) != 2 || (a[1] != "sent" && a[1] != "requeue") {
			h.reply(ctx, chat, "Проверьте, появилось ли сообщение. /resolve ID sent — уже отправлено; /resolve ID requeue — вернуть в очередь следующего дня (возможен повтор).")
			return
		}
		id, e := strconv.ParseInt(a[0], 10, 64)
		if e != nil {
			err = e
			break
		}
		err = h.Store.Resolve(ctx, u.ID, chat, id, a[1] == "sent")
	default:
		h.reply(ctx, chat, "Команды: /help")
		return
	}
	if err != nil {
		if errors.Is(err, daily.ErrDuplicate) {
			h.reply(ctx, chat, "Уже обработано или такой материал уже есть.")
			return
		}
		h.Log.Warn("command not applied", "command", cmd)
		h.reply(ctx, chat, "Не удалось применить команду. Проверьте формат, часовой пояс, ID и состояние материала. /help")
		return
	}
	h.reply(ctx, chat, "Готово. Изменения расписания действуют со следующего будущего времени публикации.")
}
func (h *Handler) callback(ctx context.Context, u *models.Update) {
	q := u.CallbackQuery
	m := q.Message.Message
	if m == nil || !h.Allowed[m.Chat.ID] || q.From.IsBot {
		return
	}
	_, _ = h.API.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: q.ID})
	if !h.admin(ctx, m.Chat.ID, q.From.ID) {
		h.reply(ctx, m.Chat.ID, "Одобрять материалы может администратор чата.")
		return
	}
	action, raw, ok := strings.Cut(q.Data, ":")
	if !ok || (action != "approve" && action != "reject") {
		return
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return
	}
	err = h.Store.Moderate(ctx, u.ID, m.Chat.ID, id, q.From.ID, action == "approve", h.Now())
	if err != nil {
		h.reply(ctx, m.Chat.ID, "Материал уже обработан или недоступен в этом чате.")
		return
	}
	h.reply(ctx, m.Chat.ID, "Решение сохранено.")
}
func (h *Handler) admin(ctx context.Context, chat, user int64) bool {
	admins, err := h.API.GetChatAdministrators(ctx, &bot.GetChatAdministratorsParams{ChatID: chat})
	if err != nil {
		return false
	}
	for _, a := range admins {
		if a.Owner != nil && a.Owner.User.ID == user {
			return true
		}
		if a.Administrator != nil && a.Administrator.User.ID == user {
			return true
		}
	}
	return false
}
func (h *Handler) reply(ctx context.Context, chat int64, text string) {
	if _, err := h.API.SendMessage(ctx, &bot.SendMessageParams{ChatID: chat, Text: text}); err != nil {
		h.Log.Warn("command reply failed", "chat_id", chat)
	}
}
func (h *Handler) problem(ctx context.Context, chat int64) {
	h.Log.Error("storage operation failed")
	h.reply(ctx, chat, "Не удалось прочитать настройки. Попробуйте позже.")
}

type Sender struct{ API API }

func (s Sender) Send(ctx context.Context, chat int64, i daily.Item) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var m *models.Message
	var err error
	if i.Kind == daily.Meme {
		text := "Мем дня\n" + i.Text
		if i.Source != "" {
			text += "\nИсточник: " + i.Source
		}
		m, err = s.API.SendPhoto(ctx, &bot.SendPhotoParams{ChatID: chat, Photo: &models.InputFileString{Data: i.Image}, Caption: text})
	} else {
		m, err = s.API.SendMessage(ctx, &bot.SendMessageParams{ChatID: chat, Text: "Факт о кино\n" + i.Text + "\nИсточник: " + i.Source})
	}
	if err != nil {
		return 0, classify(err)
	}
	if m == nil {
		return 0, &daily.SendError{Kind: "unknown"}
	}
	return m.ID, nil
}
func classify(err error) error {
	var rate *bot.TooManyRequestsError
	var migrate *bot.MigrateError
	if errors.As(err, &rate) {
		return &daily.SendError{Kind: "retry", After: time.Duration(rate.RetryAfter) * time.Second}
	}
	if errors.Is(err, bot.ErrorForbidden) || errors.Is(err, bot.ErrorUnauthorized) || errors.As(err, &migrate) {
		return &daily.SendError{Kind: "forbidden"}
	}
	if errors.Is(err, bot.ErrorBadRequest) || errors.Is(err, bot.ErrorNotFound) {
		return &daily.SendError{Kind: "permanent"}
	}
	return &daily.SendError{Kind: "unknown"}
}

package telegram

import (
	"context"
	"errors"
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/vaporon4a/movie-helper/internal/daily"
	"github.com/vaporon4a/movie-helper/internal/feedback"
)

const menuPauseAction = "pause"

type dailyMenuAction struct{ label, action string }

func dailyName(kind string) string {
	if kind == daily.Meme {
		return "🎭 Мем дня"
	}
	return "🎬 Факт дня"
}
func (h *Handler) cancelDailyPanel(ctx context.Context, chat int64, message int) error {
	if h.Interactions == nil || message <= 0 {
		return nil
	}
	prompts, err := h.Interactions.CancelPanel(ctx, chat, message, h.Now())
	if err != nil {
		return err
	}
	for _, id := range prompts {
		if e := h.deleteOrHidePanel(ctx, chat, id); e != nil {
			h.Log.DebugContext(ctx, "input prompt cleanup failed", "chat_id", chat, "message_id", id)
		}
	}
	return nil
}
func (h *Handler) dailyKindPanel(ctx context.Context, chat int64, message int, kind, notice string, confirm bool) error {
	if h.DailyMenu == nil || !daily.ValidKind(kind) {
		return daily.ErrConflict
	}
	screen, screenErr := h.Interactions.PanelScreen(ctx, chat, message)
	if screenErr != nil {
		return screenErr
	}
	if screen != "" && screen != dailyPage {
		return nil
	}
	closed, err := h.Interactions.Closed(ctx, chat, message)
	if err != nil {
		return err
	}
	if closed {
		return nil
	}
	if err := h.cancelDailyPanel(ctx, chat, message); err != nil {
		return err
	}
	c, err := h.DailyMenu.Config(ctx, chat)
	if err != nil {
		return err
	}
	text, actions := dailyPanelPresentation(c, kind, notice, confirm)
	_, unresolved, issueErr := h.DailyMenu.PreviewIssue(ctx, chat, kind, h.Now())
	if issueErr != nil {
		return issueErr
	}
	if unresolved && !confirm {
		text += "\n\n⚠️ Доставка прошлого примера не подтверждена. Проверьте сообщения в чате."
		actions = append(actions, dailyMenuAction{"🛠 Разобрать доставку", "preview_recovery"})
	}

	keyboard := &models.InlineKeyboardMarkup{}
	if err = h.dailyActionButtons(ctx, keyboard, actions, daily.MenuIntent{ChatID: chat, PanelID: message, Kind: kind, Revision: c.Revision}); err != nil {
		return err
	}
	if confirm {
		keyboard.InlineKeyboard = append(keyboard.InlineKeyboard, []models.InlineKeyboardButton{menuButton("← Отмена", "daily_"+kind)})
	}
	keyboard.InlineKeyboard = append(keyboard.InlineKeyboard, []models.InlineKeyboardButton{menuButton("← Мемы и факты", dailyPage)})
	_, err = h.API.EditMessageText(ctx, &bot.EditMessageTextParams{ChatID: chat, MessageID: message, Text: text, ReplyMarkup: closableKeyboard(keyboard), LinkPreviewOptions: disabledLinkPreview()})
	return err
}
func (h *Handler) dailyMenuCallback(ctx context.Context, q *models.CallbackQuery, m *models.Message) {
	if h.DailyMenu == nil || !h.Allowed[m.Chat.ID] || m.Chat.Type == models.ChatTypePrivate {
		return
	}
	i, err := h.Interactions.Input(ctx, strings.TrimPrefix(q.Data, "dm:"), m.Chat.ID, m.ID, h.Now())
	if err != nil {
		_, _ = h.API.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: q.ID, Text: menuErrorText(err)})
		if errors.Is(err, daily.ErrConflict) {
			h.Log.DebugContext(ctx, "daily menu intent expired", "chat_id", m.Chat.ID)
		} else {
			h.Log.WarnContext(ctx, "daily menu lookup failed", "chat_id", m.Chat.ID, "reason", "storage_unavailable")
		}
		return
	}
	_, _ = h.API.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: q.ID})
	switch i.Action {
	case "preview_recovery", "preview_seen", "preview_absent":
		err = h.previewRecoveryPanel(ctx, i, i.Action)
	case "resolve_seen", "resolve_absent":
		err = h.DailyMenu.ResolvePreview(ctx, i.ChatID, daily.PreviewIssue{Token: i.Clock, Kind: i.Kind, Revision: i.Revision}, i.Action == "resolve_seen", h.Now())
		notice := "✅ Решение сохранено. Повторной отправки не было. Новый пример можно запросить отдельным нажатием."
		if err != nil {
			notice = menuErrorText(err)
		}
		err = h.dailyKindPanel(ctx, i.ChatID, i.PanelID, i.Kind, notice, false)
	case "ask_pause":
		err = h.dailyKindPanel(ctx, m.Chat.ID, m.ID, i.Kind, "", true)
	case "ask_time":
		err = h.askDailyTime(ctx, i, q.From)
	case "preview":
		err = h.dailyMenuPreview(ctx, i)
	default:
		var changed bool
		changed, err = h.DailyMenu.Apply(ctx, i, h.Now())
		notice := "Настройка уже была установлена."
		if changed {
			notice = "✅ Изменение сохранено. Расписание действует для будущих публикаций."
		}
		if err != nil {
			notice = menuErrorText(err)
		}
		err = h.dailyKindPanel(ctx, m.Chat.ID, m.ID, i.Kind, notice, false)
	}
	if err != nil && !isUnmodified(err) {
		h.Log.WarnContext(ctx, "daily menu failed", "chat_id", m.Chat.ID, "kind", i.Kind, "action", i.Action)
	}
}
func isUnmodified(err error) bool {
	return errors.Is(err, bot.ErrorBadRequest) && strings.Contains(strings.ToLower(err.Error()), "message is not modified")
}
func (h *Handler) askDailyTime(ctx context.Context, i daily.MenuIntent, user models.User) error {
	// Retire the button before sending a prompt, preventing two physical clicks.
	if err := h.cancelDailyPanel(ctx, i.ChatID, i.PanelID); err != nil {
		return err
	}
	prompts, err := h.Interactions.CancelActorInputs(ctx, i.ChatID, user.ID)
	if err != nil {
		return err
	}
	for _, id := range prompts {
		_ = h.deleteOrHidePanel(ctx, i.ChatID, id)
	}
	i.Action = timeInputAction
	i.ActorID = user.ID
	next, err := h.Interactions.NewIntent(ctx, i, h.Now())
	if err != nil {
		return err
	}
	text := fmt.Sprintf(`<a href="tg://user?id=%d">%s</a>, во сколько публиковать? Ответьте на это сообщение временем HH:MM, например 12:30. Состояние рубрики не изменится. Запрос действует 10 минут.`, user.ID, html.EscapeString(user.FirstName))
	m, err := h.API.SendMessage(ctx, &bot.SendMessageParams{ChatID: i.ChatID, Text: text, ParseMode: models.ParseModeHTML, ReplyMarkup: &models.ForceReply{ForceReply: true, Selective: true, InputFieldPlaceholder: "Время HH:MM"}})
	if err != nil {
		return err
	}
	if m == nil || m.ID <= 0 {
		return daily.ErrConflict
	}
	return h.Interactions.BindPrompt(ctx, next.Token, m.ID)
}
func (h *Handler) handleDailyTimeReply(ctx context.Context, m *models.Message) bool {
	if h.DailyMenu == nil || m.From == nil || m.ReplyToMessage == nil {
		return false
	}
	i, err := h.Interactions.Reply(ctx, m.Chat.ID, m.From.ID, m.ReplyToMessage.ID, h.Now())
	if err != nil || !daily.ValidKind(i.Kind) {
		return false
	}
	clock := strings.TrimSpace(m.Text)
	if _, err = time.Parse("15:04", clock); err != nil || len(clock) != 5 {
		// Keep the same prompt/intent; do not add another service message.
		_, _ = h.API.EditMessageText(ctx, &bot.EditMessageTextParams{ChatID: i.ChatID, MessageID: i.PromptID, Text: "Нужно время HH:MM, например 09:30. Ответьте ещё раз на это сообщение. Запрос действует 10 минут."})
		return true
	}
	i.Clock = clock
	_, err = h.DailyMenu.Apply(ctx, i, h.Now())
	notice := "✅ Время сохранено. Состояние рубрики осталось прежним."
	if err != nil {
		notice = menuErrorText(err)
	}
	if e := h.dailyKindPanel(ctx, i.ChatID, i.PanelID, i.Kind, notice, false); e != nil {
		h.Log.WarnContext(ctx, "time result panel failed", "chat_id", i.ChatID)
	}
	_ = h.deleteOrHidePanel(ctx, i.ChatID, i.PromptID)
	return true
}
func (h *Handler) dailyMenuPreview(ctx context.Context, i daily.MenuIntent) error {
	item, found, err := h.DailyMenu.ReadyPreview(ctx, i, h.Now())
	if err != nil {
		return err
	}
	if !found {
		return h.dailyKindPanel(ctx, i.ChatID, i.PanelID, i.Kind, "⏳ Готового материала пока нет. Фоновая подготовка запущена. Нажмите «Показать пример» позже — расписание не изменилось.", false)
	}
	if err = h.DailyMenu.Claim(ctx, i, h.Now()); err != nil {
		if !errors.Is(err, daily.ErrConflict) {
			return err
		}
		return h.dailyKindPanel(ctx, i.ChatID, i.PanelID, i.Kind, "Пример уже запрошен. Между показами — 30 секунд. Если доставка не подтверждена, сначала проверьте чат; автоматического повтора не будет.", false)
	}
	item.ChatID = i.ChatID
	item.Kind = i.Kind
	if err = item.Validate(); err != nil {
		_, _ = h.DailyMenu.Finish(ctx, i, "rejected", 0, h.Now())
		return err
	}
	item.Text = "Предпросмотр · " + item.Text
	id, sendErr := (Sender{API: h.API, Feedback: h.Feedback, Publication: feedback.Preview, Log: h.Log, Now: h.Now, Closable: true}).Send(ctx, i.ChatID, item)
	state := "sent"
	if sendErr != nil {
		state = deliveryUnknown
		if e, ok := errors.AsType[*daily.SendError](sendErr); ok && e.Kind != deliveryUnknown {
			state = "rejected"
		}
	}
	persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	closed, err := h.DailyMenu.Finish(persist, i, state, id, h.Now())
	if err != nil {
		h.Log.WarnContext(ctx, "preview outcome persistence failed", "chat_id", i.ChatID)
		return err
	}
	if closed && id > 0 {
		return h.deleteOrHidePanel(persist, i.ChatID, id)
	}
	notice := "✅ Пример опубликован. Ежедневный выпуск и расписание сохранены."
	switch state {
	case deliveryUnknown:
		notice = "Доставка примера не подтверждена. Проверьте чат: автоматического повтора не будет."
	case "rejected":
		notice = "Telegram отклонил пример. Попробуйте позже."
	}
	h.Log.InfoContext(ctx, "menu preview finished", "chat_id", i.ChatID, "kind", i.Kind, "state", state)
	return h.dailyKindPanel(ctx, i.ChatID, i.PanelID, i.Kind, notice, false)
}

func (h *Handler) dailyActionButtons(ctx context.Context, k *models.InlineKeyboardMarkup, actions []dailyMenuAction, base daily.MenuIntent) error {
	for _, a := range actions {
		base.Action = a.action
		i, err := h.Interactions.NewIntent(ctx, base, h.Now())
		if err != nil {
			return err
		}
		k.InlineKeyboard = append(k.InlineKeyboard, []models.InlineKeyboardButton{{Text: a.label, CallbackData: "dm:" + i.Token}})
	}
	return nil
}

func (h *Handler) dailyScheduleCommand(ctx context.Context, op, chat int64, command, args string, now time.Time) error {
	var err error
	var notice string
	switch command {
	case "/timezone":
		err = h.Daily.SetZone(ctx, op, chat, args, now)
		notice = "✅ Часовой пояс сохранён: " + args + ". Проверьте часы в /settings."
	case "/schedule":
		fields := strings.Fields(args)
		if len(fields) != 2 {
			h.reply(ctx, chat, "Пример: /schedule meme 09:00. Сначала задайте /timezone.")
			return errResponseSent
		}
		err = h.Daily.SetSchedule(ctx, op, chat, fields[0], fields[1], true, now)
		notice = "✅ " + dailyName(fields[0]) + " включён: " + fields[1] + " по времени чата. Изменения действуют для будущих публикаций."
	case "/pause":
		err = h.Daily.SetSchedule(ctx, op, chat, args, "09:00", false, now)
		notice = "⏸ " + dailyName(args) + " приостановлен. Время сохранено; уже отправляемый пост может завершиться."
	}
	if err != nil {
		return err
	}
	h.reply(ctx, chat, notice)
	return errResponseSent
}

func dailyPanelPresentation(c daily.ConfigSnapshot, kind, notice string, confirm bool) (string, []dailyMenuAction) {
	var schedule daily.Schedule
	for _, v := range c.Schedules {
		if v.Kind == kind {
			schedule = v
		}
	}
	state := "выключен"
	if schedule.Enabled {
		state = "включён"
	}
	zone := c.Zone
	if zone == "" {
		zone = "не задан — /timezone Europe/Moscow"
	}
	text := fmt.Sprintf("%s\n\nКаждый день в %s\nЧасовой пояс: %s\nСтатус: %s", dailyName(kind), schedule.Clock, zone, state)
	if !c.Active {
		text += "\nСвязь с чатом приостановлена. После проверки доступа: /resume"
	}
	if notice != "" {
		text += "\n\n" + notice
	}
	actions := []dailyMenuAction{{"👀 Показать пример", "preview"}, {"🕒 Изменить время", "ask_time"}, {"▶️ Включить", "enable"}}
	if schedule.Enabled {
		actions[2] = dailyMenuAction{"⏸ Приостановить…", "ask_pause"}
	}
	if confirm {
		text += "\n\nПриостановить только эту рубрику? Ожидающие публикации отменятся; уже отправляемый пост может завершиться."
		actions = []dailyMenuAction{{"⏸ Да, приостановить", menuPauseAction}}
	}
	return text, actions
}

func menuErrorText(err error) string {
	if errors.Is(err, daily.ErrConflict) {
		return "Действие устарело. Откройте актуальные настройки."
	}
	return "Не удалось прочитать или сохранить данные. Попробуйте позже; действие не подтверждено."
}

func (h *Handler) previewRecoveryPanel(ctx context.Context, i daily.MenuIntent, step string) error {
	issue, found, err := h.DailyMenu.PreviewIssue(ctx, i.ChatID, i.Kind, h.Now())
	if err != nil {
		return err
	}
	if !found {
		return h.dailyKindPanel(ctx, i.ChatID, i.PanelID, i.Kind, "Проблема уже разрешена.", false)
	}
	if err = h.cancelDailyPanel(ctx, i.ChatID, i.PanelID); err != nil {
		return err
	}
	text := "🛠 Проверка доставки\n\nПосмотрите, появился ли пример в чате. Бот не может достоверно узнать это после обрыва связи."
	actions := []dailyMenuAction{{"Сообщение есть…", "preview_seen"}, {"Проверил, сообщения нет…", "preview_absent"}}
	if step != "preview_recovery" {
		text += "\n\nПодтвердить решение? Автоматической отправки не будет. Если сообщения всё же есть, новый запрос может создать дубль."
		action := "resolve_absent"
		if step == "preview_seen" {
			action = "resolve_seen"
		}
		actions = []dailyMenuAction{{"✅ Подтвердить", action}}
	}
	i.Clock = issue.Token
	i.Revision = issue.Revision
	keyboard := &models.InlineKeyboardMarkup{}
	if err = h.dailyActionButtons(ctx, keyboard, actions, i); err != nil {
		return err
	}
	keyboard.InlineKeyboard = append(keyboard.InlineKeyboard, []models.InlineKeyboardButton{menuButton("← Назад", "daily_"+i.Kind)})
	_, err = h.API.EditMessageText(ctx, &bot.EditMessageTextParams{ChatID: i.ChatID, MessageID: i.PanelID, Text: text, ReplyMarkup: closableKeyboard(keyboard)})
	return err
}

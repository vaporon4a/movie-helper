package telegram

import (
	"context"
	"errors"
	"fmt"
	"html"
	"strconv"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/vaporon4a/movie-helper/internal/clubprofile"
	"github.com/vaporon4a/movie-helper/internal/clubreminder"
	"github.com/vaporon4a/movie-helper/internal/featurevote"
	"github.com/vaporon4a/movie-helper/internal/interaction"
	"github.com/vaporon4a/movie-helper/internal/scheduleui"
)

func scheduleButton(d scheduleui.Draft, label, action string) models.InlineKeyboardButton {
	return models.InlineKeyboardButton{Text: label, CallbackData: "sc:" + d.Token + ":" + action}
}
func (h *Handler) beginSchedule(ctx context.Context, chat int64, panel int, kind string) error {
	if h.ScheduleUI == nil {
		return errors.New("schedule editor unavailable")
	}
	d, err := h.ScheduleUI.Begin(ctx, chat, panel, kind, h.Now())
	if err != nil {
		return err
	}
	return h.renderSchedule(ctx, d, "")
}
func (h *Handler) scheduleCallback(ctx context.Context, q *models.CallbackQuery, m *models.Message) {
	if h.ScheduleUI == nil || !h.Allowed[m.Chat.ID] || m.Chat.Type == models.ChatTypePrivate {
		return
	}
	token, action, ok := strings.Cut(strings.TrimPrefix(q.Data, "sc:"), ":")
	if !ok {
		return
	}
	d, err := h.ScheduleUI.Read(ctx, token, m.Chat.ID, m.ID, h.Now())
	if err != nil {
		h.answerUIError(ctx, q.ID, err)
		return
	}
	_, _ = h.API.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: q.ID})
	if action == "save" {
		h.saveSchedule(ctx, d)
		return
	}
	if err = h.cancelDailyPanel(ctx, m.Chat.ID, m.ID); err != nil {
		return
	}
	if action == timeInputAction {
		h.askScheduleTime(ctx, d, q.From)
		return
	}
	if !changeScheduleDraft(&d, action) {
		return
	}
	next, err := h.ScheduleUI.Update(ctx, d, h.Now())
	if err == nil {
		err = h.renderSchedule(ctx, next, "")
	}
	if err != nil {
		h.Log.WarnContext(ctx, "schedule dialog failed", "chat_id", m.Chat.ID, "reason", "state_or_delivery")
	}
}
func changeScheduleDraft(d *scheduleui.Draft, action string) bool {
	if action == "review" {
		d.Step = "confirm"
		return true
	}
	if action == "back" {
		d.Step = editPage
		return true
	}
	if action == "day_toggle" && d.Step == "day" && d.Movies != nil {
		d.Movies.Days[d.Day].Enabled = !d.Movies.Days[d.Day].Enabled
		return true
	}
	if d.Step != editPage {
		return false
	}
	if strings.HasPrefix(action, "day_") {
		return selectScheduleDay(d, strings.TrimPrefix(action, "day_"))
	}
	if strings.HasPrefix(action, "ordinal_") {
		return selectScheduleOrdinal(d, strings.TrimPrefix(action, "ordinal_"))
	}

	if action == "toggle" {
		switch {
		case d.Movies != nil:
			d.Movies.Days[d.Day].Enabled = !d.Movies.Days[d.Day].Enabled
		case d.Club != nil:
			d.Club.Value.Enabled = !d.Club.Value.Enabled
		case d.Ideas != nil:
			d.Ideas.Value.Enabled = !d.Ideas.Value.Enabled
		}
		return true
	}
	return false
}
func (h *Handler) saveSchedule(ctx context.Context, d scheduleui.Draft) {
	changed, err := h.ScheduleUI.Commit(ctx, d, h.Now())
	text := "✅ Настройки сохранены для будущих событий. Открытые опросы продолжаются."
	if !changed {
		text = "Настройки уже были установлены. Расписание сохранено."
	}
	if err != nil {
		text = "Не удалось сохранить. Откройте расписание заново: настройки могли измениться."
		if !errors.Is(err, scheduleui.ErrConflict) && !errors.Is(err, clubprofile.ErrConfigConflict) && !errors.Is(err, clubreminder.ErrConfigConflict) && !errors.Is(err, featurevote.ErrConfigConflict) {
			text = "Хранилище временно недоступно. Сохранение не подтверждено."
		}
		h.Log.WarnContext(ctx, "schedule save declined", "chat_id", d.ChatID, "kind", d.Kind, "reason", "conflict_or_storage")
	} else {
		h.Log.InfoContext(ctx, "schedule dialog saved", "chat_id", d.ChatID, "kind", d.Kind, "changed", changed)
	}
	_, _ = h.API.EditMessageText(ctx, &bot.EditMessageTextParams{ChatID: d.ChatID, MessageID: d.PanelID, Text: text, ReplyMarkup: closableKeyboard(menuBack())})
}
func scheduleState(enabled bool) string {
	if enabled {
		return "▶️ включено"
	}
	return "⏸ выключено"
}
func scheduleDraftText(d scheduleui.Draft) string {
	var b strings.Builder
	switch {
	case d.Movies != nil:
		fmt.Fprintf(&b, "🗓 Киноопросы · %s\n🌍 %s\n\n", d.Kind, d.Movies.Before.Zone)
		for day := 1; day <= 7; day++ {
			n := day % 7
			v := d.Movies.Days[n]
			fmt.Fprintf(&b, "%s · %s · %s\n", featurevote.WeekdayName(n), v.Clock, scheduleState(v.Enabled))
		}
		b.WriteString("\nВыберите день, чтобы изменить время или включение. Остальные дни сохраняют свои часы.")
	case d.Club != nil:
		v := d.Club.Value
		fmt.Fprintf(&b, "🍿 Встречи и напоминания\n🌍 %s\n\nВстреча: %s, неделя %d месяца (5 = последняя)\nНапоминания: %s\n%s\n\nЭто время напоминаний, не начало встречи. Два напоминания между встречами; готовые тексты сохраняются.", d.Club.Before.Zone, featurevote.WeekdayName(v.Weekday), v.Ordinal, v.Clock, scheduleState(v.Enabled))
	case d.Ideas != nil:
		v := d.Ideas.Value
		fmt.Fprintf(&b, "💡 Голосование за идеи\n🌍 %s\n\n%s · %s\n%s\n\nГолосование идёт 24 часа. Текущее голосование и бэклог сохраняются.", d.Ideas.Before.Zone, featurevote.WeekdayName(v.Weekday), v.Clock, scheduleState(v.Enabled))
	}
	if d.Step == "confirm" {
		b.WriteString("\n\nПроверьте изменения ниже. Ожидающие будущие задания будут перепланированы. Уже отправляемая публикация может завершиться.\n")
		b.WriteString(scheduleDiff(d))
	} else {
		b.WriteString("\n\nЧерновик: ничего не применяется до «Сохранить». Закрытие отменяет изменения.")
	}
	return b.String()
}
func scheduleDiff(d scheduleui.Draft) string {
	var b strings.Builder
	switch {
	case d.Movies != nil:
		before := [7]scheduleui.Day{}
		for day := range before {
			before[day].Clock = "09:00"
		}
		for _, s := range d.Movies.Before.Schedules {
			if s.Topic == d.Movies.Topic {
				before[s.Weekday] = scheduleui.Day{Clock: s.Clock, Enabled: s.Enabled}
			}
		}
		for day, v := range d.Movies.Days {
			old := before[day]
			if old != v {
				fmt.Fprintf(&b, "• %s: %s %s → %s %s\n", featurevote.WeekdayName(day), old.Clock, scheduleState(old.Enabled), v.Clock, scheduleState(v.Enabled))
			}
		}
	case d.Club != nil:
		old, next := d.Club.Before.Settings, d.Club.Value
		fmt.Fprintf(&b, "Было: %s/%d, %s, %s\nБудет: %s/%d, %s, %s", featurevote.WeekdayName(old.Weekday), old.Ordinal, old.Clock, scheduleState(old.Enabled), featurevote.WeekdayName(next.Weekday), next.Ordinal, next.Clock, scheduleState(next.Enabled))
	case d.Ideas != nil:
		old, next := d.Ideas.Before.Schedule, d.Ideas.Value
		fmt.Fprintf(&b, "Было: %s %s %s\nБудет: %s %s %s", featurevote.WeekdayName(old.Weekday), old.Clock, scheduleState(old.Enabled), featurevote.WeekdayName(next.Weekday), next.Clock, scheduleState(next.Enabled))
	}
	if b.Len() == 0 {
		return "Изменений нет."
	}
	return b.String()
}
func scheduleKeyboard(d scheduleui.Draft) *models.InlineKeyboardMarkup {
	k := &models.InlineKeyboardMarkup{}
	switch d.Step {
	case "confirm":
		k.InlineKeyboard = append(k.InlineKeyboard, []models.InlineKeyboardButton{scheduleButton(d, "✅ Сохранить", "save"), scheduleButton(d, "← Изменить", "back")})
	case "day":
		k.InlineKeyboard = append(k.InlineKeyboard, []models.InlineKeyboardButton{scheduleButton(d, "🕒 Время для "+featurevote.WeekdayName(d.Day), timeInputAction), scheduleButton(d, "Включить / выключить", "day_toggle")}, []models.InlineKeyboardButton{scheduleButton(d, "← Все дни", "back")})
	default:
		row := []models.InlineKeyboardButton{}
		for day := 1; day <= 7; day++ {
			n := day % 7
			row = append(row, scheduleButton(d, featurevote.WeekdayName(n), fmt.Sprintf("day_%d", n)))
		}
		k.InlineKeyboard = append(k.InlineKeyboard, row)
		if d.Club != nil {
			row = nil
			for n := 1; n <= 5; n++ {
				label := strconv.Itoa(n)
				if n == 5 {
					label = "последняя"
				}
				row = append(row, scheduleButton(d, label, fmt.Sprintf("ordinal_%d", n)))
			}
			k.InlineKeyboard = append(k.InlineKeyboard, row)
		}
		if d.Movies == nil {
			k.InlineKeyboard = append(k.InlineKeyboard, []models.InlineKeyboardButton{scheduleButton(d, "🕒 Время", timeInputAction), scheduleButton(d, "Включить / выключить", "toggle")})
		}
		k.InlineKeyboard = append(k.InlineKeyboard, []models.InlineKeyboardButton{scheduleButton(d, "Проверить изменения →", "review")})
	}
	k.InlineKeyboard = append(k.InlineKeyboard, []models.InlineKeyboardButton{menuButton("← Настройки", "home")})
	return closableKeyboard(k)
}
func (h *Handler) renderSchedule(ctx context.Context, d scheduleui.Draft, notice string) error {
	_, err := h.API.EditMessageText(ctx, &bot.EditMessageTextParams{ChatID: d.ChatID, MessageID: d.PanelID, Text: limitMenuText(scheduleDraftText(d) + "\n" + notice), ReplyMarkup: scheduleKeyboard(d)})
	return err
}
func (h *Handler) askScheduleTime(ctx context.Context, d scheduleui.Draft, user models.User) {
	next, err := h.ScheduleUI.Update(ctx, d, h.Now())
	if err != nil {
		return
	}
	if err = h.renderSchedule(ctx, next, "Ответьте на адресный запрос времени."); err != nil {
		return
	}
	i := interaction.Intent{ChatID: d.ChatID, PanelID: d.PanelID, Kind: "schedule", Action: timeInputAction, ActorID: user.ID, Clock: next.Token}
	_ = h.promptInput(ctx, i, user, "Во сколько? Ответьте временем HH:MM, например 09:30. Включение не изменится.")
}
func (h *Handler) promptInput(ctx context.Context, i interaction.Intent, user models.User, text string) error {
	if h.Interactions == nil {
		return errors.New("input unavailable")
	}
	if _, err := h.Interactions.CancelActorInputs(ctx, i.ChatID, user.ID); err != nil {
		return err
	}
	next, err := h.Interactions.NewIntent(ctx, i, h.Now())
	if err != nil {
		return err
	}
	m, err := h.API.SendMessage(ctx, &bot.SendMessageParams{ChatID: i.ChatID, Text: fmt.Sprintf(`<a href="tg://user?id=%d">%s</a>, %s`+"\nЗапрос действует 10 минут.", user.ID, html.EscapeString(user.FirstName), html.EscapeString(text)), ParseMode: models.ParseModeHTML, ReplyMarkup: &models.ForceReply{ForceReply: true, Selective: true}})
	if err != nil {
		return err
	}
	if m == nil || m.ID <= 0 {
		return errors.New("missing input receipt")
	}
	return h.Interactions.BindPrompt(ctx, next.Token, m.ID)
}
func (h *Handler) handleScheduleReply(ctx context.Context, m *models.Message) bool {
	if h.Interactions == nil || h.ScheduleUI == nil || m.From == nil || m.ReplyToMessage == nil {
		return false
	}
	i, err := h.Interactions.Reply(ctx, m.Chat.ID, m.From.ID, m.ReplyToMessage.ID, h.Now())
	if err != nil || i.Kind != "schedule" {
		return false
	}
	clock := strings.TrimSpace(m.Text)
	if _, err = time.Parse("15:04", clock); err != nil || len(clock) != 5 {
		_, _ = h.API.EditMessageText(ctx, &bot.EditMessageTextParams{ChatID: i.ChatID, MessageID: i.PromptID, Text: "Нужно время HH:MM, например 09:30. Ответьте ещё раз."})
		return true
	}
	d, err := h.ScheduleUI.Read(ctx, i.Clock, i.ChatID, i.PanelID, h.Now())
	if err != nil {
		return true
	}
	switch {
	case d.Movies != nil:
		d.Movies.Days[d.Day].Clock = clock
	case d.Club != nil:
		d.Club.Value.Clock = clock
	case d.Ideas != nil:
		d.Ideas.Value.Clock = clock
	}
	d, err = h.ScheduleUI.Update(ctx, d, h.Now())
	if err == nil {
		_ = h.cancelDailyPanel(ctx, i.ChatID, i.PanelID)
		_ = h.renderSchedule(ctx, d, "Время в черновике. Проверьте и сохраните изменения.")
	}
	return true
}
func (h *Handler) answerUIError(ctx context.Context, query string, err error) {
	text := "Не удалось прочитать данные. Попробуйте позже."
	if errors.Is(err, scheduleui.ErrConflict) {
		text = "Панель устарела. Откройте настройки заново."
	}
	_, _ = h.API.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: query, Text: text})
	h.Log.DebugContext(ctx, "UI action declined", "reason", "expired_or_storage")
}

func selectScheduleDay(d *scheduleui.Draft, value string) bool {
	day, err := strconv.Atoi(value)
	if err != nil || day < 0 || day > 6 {
		return false
	}
	switch {
	case d.Movies != nil:
		d.Day = day
		d.Step = "day"
	case d.Club != nil:
		d.Club.Value.Weekday = day
	case d.Ideas != nil:
		d.Ideas.Value.Weekday = day
	}
	return true
}
func selectScheduleOrdinal(d *scheduleui.Draft, value string) bool {
	if d.Club == nil {
		return false
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 1 || n > 5 {
		return false
	}
	d.Club.Value.Ordinal = n
	return true
}

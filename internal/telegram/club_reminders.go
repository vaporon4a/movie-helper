package telegram

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/vaporon4a/movie-helper/internal/clubreminder"
	"github.com/vaporon4a/movie-helper/internal/movieclub"
)

type ClubSender struct{ API API }

func (s ClubSender) SendReminder(ctx context.Context, chat int64, text string) (int, error) {
	message, err := s.API.SendMessage(ctx, &bot.SendMessageParams{ChatID: chat, Text: text, LinkPreviewOptions: disabledLinkPreview()})
	if err != nil {
		return 0, classify(err)
	}
	if message == nil {
		return 0, classify(fmt.Errorf("missing Telegram message"))
	}
	return message.ID, nil
}
func (h *Handler) handleClubCommand(ctx context.Context, update *models.Update, command, args string) (bool, error) {
	switch command {
	case "/club_schedule", "/club_settings", "/club_pause", "/club_preview", "/club_resolve":
	default:
		return false, nil
	}
	chat := update.Message.Chat.ID
	if h.Club == nil {
		h.reply(ctx, chat, "Напоминания пока недоступны.")
		return true, errResponseSent
	}
	switch command {
	case "/club_schedule":
		return true, h.setClubSchedule(ctx, update.ID, chat, args)
	case "/club_pause":
		return true, h.Club.Pause(ctx, update.ID, chat)
	case "/club_settings":
		settings, err := h.Club.Settings(ctx, chat)
		if err != nil {
			return true, err
		}
		h.reply(ctx, chat, clubSettingsText(settings, h.Now()))
		return true, errResponseSent
	case "/club_preview":
		return true, h.clubPreview(ctx, chat)
	case "/club_resolve":
		return true, h.resolveClub(ctx, update.ID, chat, args)
	}
	return false, nil
}
func (h *Handler) clubPreview(ctx context.Context, chat int64) error {
	text, err := h.Club.Preview(ctx, chat)
	if err != nil {
		return err
	}
	h.reply(ctx, chat, "🍿 Предпросмотр · "+clubTone(text.Tone)+" · "+text.Provider+"\n\n"+text.Body)
	return errResponseSent
}
func (h *Handler) resolveClub(ctx context.Context, op, chat int64, args string) error {
	fields := strings.Fields(args)
	if len(fields) != 2 {
		h.reply(ctx, chat, "Формат: /club_resolve ID sent|retry|cancel")
		return errResponseSent
	}
	id, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return err
	}
	return h.Club.Resolve(ctx, op, chat, id, fields[1])
}
func (h *Handler) setClubSchedule(ctx context.Context, op, chat int64, args string) error {
	fields := strings.Fields(args)
	if len(fields) != 3 {
		h.reply(ctx, chat, "Формат: /club_schedule fri 2 19:00\nДень: mon..sun, неделя: 1..4 или last. Время — для напоминаний. Сначала задайте /timezone.")
		return errResponseSent
	}
	weekday, ok := movieclub.ParseWeekday(fields[0])
	ordinal, err := strconv.Atoi(fields[1])
	if fields[1] == "last" {
		ordinal, err = 5, nil
	}
	if !ok || err != nil || fields[1] == "5" || clubreminder.Validate(weekday, ordinal, fields[2]) != nil {
		h.reply(ctx, chat, "Укажите день mon..sun, неделю 1..4 или last и время HH:MM.")
		return errResponseSent
	}
	if err = h.Club.SetSchedule(ctx, op, chat, weekday, ordinal, fields[2]); err != nil {
		return err
	}
	settings, err := h.Club.Settings(ctx, chat)
	if err != nil {
		return err
	}
	h.reply(ctx, chat, clubSettingsText(settings, h.Now()))
	return errResponseSent
}
func clubSettingsText(settings clubreminder.Settings, now time.Time) string {
	if settings.Revision == 0 {
		return "🍿 Напоминания выключены.\nВключить: /club_schedule fri 2 19:00"
	}
	ordinal := strconv.Itoa(settings.Ordinal)
	if settings.Ordinal == 5 {
		ordinal = "последняя"
	}
	state := "выключено"
	if settings.Enabled {
		state = "включено"
	}
	lines := []string{"🍿 Напоминания о выборе фильма", fmt.Sprintf("Встреча: %s неделя · %s", ordinal, []string{"вс", "пн", "вт", "ср", "чт", "пт", "сб"}[settings.Weekday]), fmt.Sprintf("Напоминания: +10 и +17 дней · %s · %s · %s", settings.Clock, settings.Zone, state), fmt.Sprintf("Готовых текстов: %d/%d · случайный тон", settings.Stock, clubreminder.Target), "Если AI недоступен, используется запасной текст."}
	if settings.LastAIError != "" {
		lines = append(lines, "Пополнение буфера: "+settings.LastAIError)
	}
	if settings.NextRefill > now.Unix() {
		loc, err := time.LoadLocation(settings.Zone)
		if err == nil {
			lines = append(lines, "Следующая попытка AI: "+time.Unix(settings.NextRefill, 0).In(loc).Format("02.01 15:04"))
		}
	}
	if settings.Enabled {
		lines = append(lines, clubUpcoming(settings, now)...)
	}
	lines = append(lines, clubIssues(settings.Issues)...)

	return strings.Join(lines, "\n")
}

func clubUpcoming(settings clubreminder.Settings, now time.Time) []string {
	loc, err := time.LoadLocation(settings.Zone)
	if err != nil {
		return nil
	}
	local := now.In(loc)
	meeting := clubreminder.Meeting(local.Year(), local.Month(), settings.Weekday, settings.Ordinal, loc)
	if meeting.Format("2006-01-02") < local.Format("2006-01-02") {
		base := time.Date(local.Year(), local.Month()+1, 1, 12, 0, 0, 0, loc)
		meeting = clubreminder.Meeting(base.Year(), base.Month(), settings.Weekday, settings.Ordinal, loc)
	}
	lines := []string{"Следующая расчётная встреча: " + meeting.Format("02.01.2006"), "Ближайшие напоминания:"}
	slots, err := clubreminder.Slots(settings, now)
	if err != nil {
		return lines
	}
	count := 0
	for _, slot := range slots {
		if slot.At.After(now) && slot.At.Unix() > settings.Effective {
			lines = append(lines, "• "+slot.At.Format("02.01.2006 15:04"))
			count++
			if count == 2 {
				break
			}
		}
	}
	return lines
}
func clubIssues(issues []clubreminder.Delivery) []string {
	lines := make([]string, 0, len(issues)*2)
	for _, d := range issues {
		lines = append(lines, fmt.Sprintf("Доставка #%d · %s · %s", d.ID, d.State, d.Error))
		if d.State == clubreminder.Unknown {
			lines = append(lines, fmt.Sprintf("Проверьте чат: /club_resolve %d sent|retry|cancel", d.ID))
		}
	}
	return lines
}

func clubTone(tone string) string {
	switch tone {
	case "friendly":
		return "дружеский"
	case "dramatic":
		return "театральный"
	case "sarcastic":
		return "саркастичный"
	case "profane":
		return "матерный юмор"
	case "roast":
		return "roast киноклуба"
	default:
		return tone
	}
}

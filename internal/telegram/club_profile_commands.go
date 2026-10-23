package telegram

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-telegram/bot/models"
	"github.com/vaporon4a/movie-helper/internal/clubprofile"
)

func (h *Handler) handleProfilePoll(ctx context.Context, u *models.Update) {
	p := u.Poll
	if !p.IsClosed {
		return
	}
	err := clubprofile.ErrNotFound
	if h.Profile != nil {
		err = h.Profile.PollClosed(ctx, profileResult(p, clubprofile.SourceUpdate, u.ID, h.Now()))
	}
	if err == nil || errors.Is(err, clubprofile.ErrConflict) {
		return
	}
	if !errors.Is(err, clubprofile.ErrNotFound) {
		h.Log.WarnContext(ctx, "profile poll update rejected", "reason", "storage_or_invalid_final")
		return
	}
	if h.Features != nil {
		counts := make([]int, len(p.Options))
		for i, o := range p.Options {
			counts[i] = o.VoterCount
		}
		if err = h.Features.PollClosed(ctx, p.ID, counts); err != nil {
			h.Log.WarnContext(ctx, "feature poll update not applied")
		}
	}
}

func (h *Handler) handleProfileCommand(ctx context.Context, u *models.Update, command, args string) (bool, error) {
	if h.Watches != nil {
		if handled, err := h.handleWatchCommand(ctx, u, command, args); handled {
			return true, err
		}
	}
	if !isMovieCommand(command) && command != "/movie_profile" {
		return false, nil
	}
	chat := u.Message.Chat.ID
	if command == "/movie_settings" {
		return true, h.showMenu(ctx, chat, 0, "movies", false)
	}
	if h.Profile == nil {
		h.reply(ctx, chat, "Профиль киноклуба временно недоступен.")
		return true, errResponseSent
	}
	switch command {
	case "/genre_poll", referencePollCommand:
		return true, h.startProfilePoll(ctx, u, command, args)
	case "/movie_schedule":
		return true, h.setProfileSchedule(ctx, u, args, false)
	case "/movie_pause":
		return true, h.setProfileSchedule(ctx, u, args, true)
	case "/movie_taste", "/movie_profile":
		_, polls, err := h.Profile.Settings(ctx, chat)
		if err != nil {
			return true, err
		}
		text := profileEvidenceText(polls)
		h.reply(ctx, chat, text)
		return true, errResponseSent
	case "/movie_personalization", "/movie_taste_reset":
		h.reply(ctx, chat, "Автоматические рекомендации отключены. Голосования сохраняются в профиле клуба; прежние команды персонализации и сброса его не меняют. /movie_profile")
		return true, errResponseSent
	case "/movie_resolve":
		return true, h.resolveProfilePoll(ctx, u, args)
	default:
		return false, nil
	}
}

func profileStatusText(status clubprofile.ProfileStatus) string {
	var b strings.Builder
	state := scheduleDisabledText
	if status.AutoReview {
		state = scheduleEnabledText
	}
	fmt.Fprintf(&b, "\nАвтообсуждение новых просмотров: %s\n/movie_auto_review on|off\nВ исследовательском слоте до двух вопросов; обычные опросы сохраняются.\nУдаление новых опросов — после сохранения итогов, в пределах 47 часов от отправки.\n", state)
	for _, j := range status.CleanupIssues {
		fmt.Fprintf(&b, "Опрос #%d: удаление %s (%s); ответы сохранены.\n", j.PollID, j.State, j.Reason)
	}
	return b.String()
}

func (h *Handler) startProfilePoll(ctx context.Context, u *models.Update, command, args string) error {
	duration := clubprofile.DefaultDuration
	if strings.TrimSpace(args) != "" {
		var err error
		duration, err = time.ParseDuration(strings.TrimSpace(args))
		if err != nil {
			return err
		}
	}
	topic := clubprofile.Genre
	if command == referencePollCommand {
		topic = clubprofile.Reference
	}
	id, err := h.Profile.Start(ctx, topic, u.ID, u.Message.Chat.ID, duration)
	if err != nil {
		return err
	}
	h.reply(ctx, u.Message.Chat.ID, fmt.Sprintf("🎬 Опрос #%d запланирован на %s Ответы пополнят профиль клуба.", id, clubprofile.DurationText(duration)))
	return errResponseSent
}

func (h *Handler) setProfileSchedule(ctx context.Context, u *models.Update, args string, pause bool) error {
	f := strings.Fields(args)
	if len(f) == 0 {
		return clubprofile.ErrInvalid
	}
	topic := clubprofile.Topic(f[0])
	if pause && len(f) == 1 {
		return h.Profile.PauseSchedules(ctx, u.ID, u.Message.Chat.ID, topic)
	}
	want := 3
	if pause {
		want = 2
	}
	if len(f) != want {
		return clubprofile.ErrInvalid
	}
	weekday, ok := parseWeekday(f[1])
	if !ok {
		return clubprofile.ErrInvalid
	}
	clock := "00:00"
	if !pause {
		clock = f[2]
	}
	return h.Profile.SetSchedule(ctx, u.ID, clubprofile.Schedule{ChatID: u.Message.Chat.ID, Topic: topic, Weekday: weekday, Clock: clock, Enabled: !pause})
}

func (h *Handler) resolveProfilePoll(ctx context.Context, u *models.Update, args string) error {
	f := strings.Fields(args)
	if len(f) != 2 {
		return clubprofile.ErrInvalid
	}
	id, err := strconv.ParseInt(f[0], 10, 64)
	if err != nil {
		return err
	}
	if err = h.Profile.Resolve(ctx, u.ID, u.Message.Chat.ID, id, f[1]); err != nil {
		return err
	}
	h.reply(ctx, u.Message.Chat.ID, "Состояние опроса обновлено. Сохранённые результаты остаются в профиле.")
	return errResponseSent
}

func profileSettingsText(schedules []clubprofile.Schedule, polls []clubprofile.Poll) string {
	var out strings.Builder
	out.WriteString("🎬 Киноопросы для профиля клуба\n")
	if len(schedules) == 0 {
		out.WriteString("Расписание не настроено.\n")
	}
	for _, s := range schedules {
		status := scheduleDisabledText
		if s.Enabled {
			status = scheduleEnabledText
		}
		fmt.Fprintf(&out, "%s · %s %s · %s — %s\n", profileTopicName(s.Topic), weekdayName(s.Weekday), s.Clock, s.Zone, status)
	}
	count := 0
	for i := len(polls) - 1; i >= 0 && count < 8; i-- {
		p := polls[i]
		if p.State == clubprofile.Completed || p.State == clubprofile.Cancelled {
			continue
		}
		count++
		fmt.Fprintf(&out, "Опрос #%d · %s · %s", p.ID, profileTopicName(p.Question.Topic), p.State)
		if p.ErrorCode != "" {
			fmt.Fprintf(&out, " (%s)", p.ErrorCode)
		}
		if p.State == clubprofile.Open {
			fmt.Fprintf(&out, " · закрытие %s", p.ClosesAt.UTC().Format(time.RFC3339))
		}
		out.WriteByte('\n')
	}
	out.WriteString("Итоги сохраняются без подборок, постеров и отдельных сообщений.")
	return out.String()
}

func profileTopicName(topic clubprofile.Topic) string {
	switch topic {
	case clubprofile.Genre:
		return "Жанры"
	case clubprofile.Reference:
		return "Фильмы"
	case clubprofile.PostWatch:
		return "После просмотра"
	default:
		return "Опрос"
	}
}

// This first view exposes evidence; the post-watch projection is added separately.
func profileEvidenceText(polls []clubprofile.Poll) string {
	var out strings.Builder
	out.WriteString("🎯 Профиль киноклуба · интересы\n")
	complete, legacy := 0, 0
	for _, p := range polls {
		if p.State == clubprofile.Completed {
			if p.Question.Intent == clubprofile.LegacyInterest {
				legacy++
			} else {
				complete++
			}
		}
	}
	fmt.Fprintf(&out, "Новых завершённых опросов: %d\nИсторических результатов: %d\n", complete, legacy)
	out.WriteString("Это предпочтения в опросах, а не оценки просмотренного кино.\n")
	n := 0
	for i := len(polls) - 1; i >= 0 && n < 2; i-- {
		p := polls[i]
		if p.State != clubprofile.Completed || p.Result == nil {
			continue
		}
		n++
		appendProfileResult(&out, p)
	}
	return out.String()
}

func appendProfileResult(out *strings.Builder, p clubprofile.Poll) {
	if !p.Result.Closed {
		out.WriteString("\nФинал ещё не подтверждён; ниже сохранённые промежуточные счётчики.\n")
	}
	fmt.Fprintf(out, "\n%s · опрос #%d\n", profileTopicName(p.Question.Topic), p.ID)
	if p.Question.Text != "" {
		out.WriteString(p.Question.Text + "\n")
	}
	if p.Result.Total != nil {
		fmt.Fprintf(out, "Ответов: %d\n", *p.Result.Total)
	} else {
		out.WriteString("Старый результат: общее число ответивших не сохранено.\n")
	}
	for i, o := range p.Question.Options {
		if i < len(p.Result.Counts) {
			fmt.Fprintf(out, "• %s — %d\n", o.Text, p.Result.Counts[i])
		}
	}
}

const referencePollCommand = "/reference_poll"

func isMovieCommand(command string) bool {
	switch command {
	case "/genre_poll", referencePollCommand, "/movie_schedule", "/movie_pause", "/movie_settings", "/movie_resolve", "/movie_personalization", "/movie_taste", "/movie_taste_reset":
		return true
	default:
		return false
	}
}

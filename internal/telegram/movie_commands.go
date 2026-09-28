package telegram

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/vaporon4a/movie-helper/internal/movieclub"
)

func (h *Handler) handleMovieCommand(ctx context.Context, update *models.Update, command, args string) (bool, error) {
	if !isMovieCommand(command) {
		return false, nil
	}
	chatID := update.Message.Chat.ID
	if h.MovieClub == nil {
		h.reply(ctx, chatID, "Киноопросы выключены: на сервере не настроен TMDB_API_TOKEN.")
		return true, errResponseSent
	}
	switch command {
	case "/genre_poll":
		return true, h.startMoviePoll(ctx, movieclub.Genre, update.ID, chatID, args)
	case "/reference_poll":
		return true, h.startMoviePoll(ctx, movieclub.Reference, update.ID, chatID, args)
	case "/movie_schedule":
		return true, h.setMovieSchedule(ctx, update.ID, chatID, args)
	case "/movie_pause":
		return true, h.pauseMovieSchedule(ctx, update.ID, chatID, args)
	case "/movie_settings":
		settings, err := h.MovieClub.Settings(ctx, chatID)
		if err == nil {
			h.reply(ctx, chatID, movieSettingsText(settings))
			return true, errResponseSent
		}
		return true, err
	case "/movie_resolve":
		return true, h.resolveMovieRound(ctx, update.ID, chatID, args)
	case "/movie_personalization":
		mode := movieclub.PersonalizationMode(strings.ToLower(strings.TrimSpace(args)))
		if !mode.Valid() {
			h.reply(ctx, chatID, "Формат: /movie_personalization off|shadow|on.")
			return true, errResponseSent
		}
		if err := h.MovieClub.SetPersonalization(ctx, update.ID, chatID, mode); err != nil {
			return true, err
		}
		h.reply(ctx, chatID, personalizationChangedText(mode))
		return true, errResponseSent
	case "/movie_taste":
		settings, profile, err := h.MovieClub.Taste(ctx, chatID)
		if err != nil {
			return true, err
		}
		h.reply(ctx, chatID, movieTasteText(settings, profile))
		return true, errResponseSent
	case "/movie_taste_reset":
		if strings.TrimSpace(args) != "" {
			h.reply(ctx, chatID, "Формат: /movie_taste_reset")
			return true, errResponseSent
		}
		_, err := h.API.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID,
			Text:        "Сбросить накопленный профиль вкусов этого чата? История опросов останется в базе.",
			ReplyMarkup: tasteResetKeyboard()})
		return true, responseResult(err)
	default:
		return false, nil
	}
}

func isMovieCommand(command string) bool {
	switch command {
	case "/genre_poll", "/reference_poll", "/movie_schedule", "/movie_pause", "/movie_settings", "/movie_resolve",
		"/movie_personalization", "/movie_taste", "/movie_taste_reset":
		return true
	default:
		return false
	}
}

func personalizationChangedText(mode movieclub.PersonalizationMode) string {
	switch mode {
	case movieclub.PersonalizationOn:
		return "Персонализация включена: новые опросы и подборки учитывают историю голосований чата."
	case movieclub.PersonalizationShadow:
		return "Режим наблюдения включён: бот сравнивает алгоритмы, но публикует прежнюю выдачу."
	default:
		return "Персонализация выключена: используется базовая ротация. История продолжает сохраняться."
	}
}

func tasteResetKeyboard() *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{
		{Text: "Сбросить", CallbackData: "movie_taste_reset:confirm"},
		{Text: "Отмена", CallbackData: "movie_taste_reset:cancel"},
	}}}
}

func movieTasteText(settings movieclub.PreferenceSettings, profile movieclub.ChatTasteProfile) string {
	type item struct {
		id    int64
		value float64
	}
	items := make([]item, 0, len(profile.GenreAffinity))
	for id, value := range profile.GenreAffinity {
		if value > 0 {
			items = append(items, item{id, value})
		}
	}
	slices.SortStableFunc(items, func(a, b item) int {
		if a.value > b.value {
			return -1
		}
		if a.value < b.value {
			return 1
		}
		return int(a.id - b.id)
	})
	lines := []string{fmt.Sprintf("🎯 Профиль киноклуба · %s", settings.Mode),
		fmt.Sprintf("Завершённых опросов: %d", profile.CompletedRounds),
		fmt.Sprintf("Эффективных голосов: %.1f", profile.EffectiveVotes)}
	if len(items) == 0 {
		lines = append(lines, "Предпочтения появятся после завершённых голосований.")
	} else {
		lines = append(lines, "Любимые жанры:")
		for _, value := range items[:min(5, len(items))] {
			label := movieclub.GenreLabel(value.id)
			if label == "" {
				label = fmt.Sprintf("жанр %d", value.id)
			}
			lines = append(lines, fmt.Sprintf("• %s · %.0f%%", label, value.value*100))
		}
	}
	lines = append(lines, "Политика: "+settings.PolicyVersion)
	return strings.Join(lines, "\n")
}

func responseResult(err error) error {
	if err != nil {
		return err
	}
	return errResponseSent
}

func (h *Handler) startMoviePoll(ctx context.Context, feature movieclub.Feature, operationID, chatID int64, args string) error {
	duration := 24 * time.Hour
	if args != "" {
		parsed, err := time.ParseDuration(args)
		if err != nil {
			command := "/genre_poll"
			if feature == movieclub.Reference {
				command = "/reference_poll"
			}
			h.reply(ctx, chatID, "Формат: "+command+" 10m. Допустимо от 5m до 24h.")
			return errResponseSent
		}
		duration = parsed
	}
	roundID, err := h.MovieClub.Start(ctx, feature, operationID, chatID, duration)
	if err == nil {
		kind := "Опрос жанров"
		if feature == movieclub.Reference {
			kind = "Опрос по фильму-ориентиру"
		}
		h.reply(ctx, chatID, fmt.Sprintf("🎬 %s #%d запланирован на %s.", kind, roundID, duration))
		return errResponseSent
	}
	return err
}

func (h *Handler) setMovieSchedule(ctx context.Context, operationID, chatID int64, args string) error {
	fields := strings.Fields(args)
	if len(fields) != 3 {
		h.reply(ctx, chatID, "Формат: /movie_schedule genre|reference wed 19:00. Сначала задайте /timezone.")
		return errResponseSent
	}
	feature := movieclub.Feature(fields[0])
	if !feature.Valid() {
		h.reply(ctx, chatID, "Тип опроса: genre или reference.")
		return errResponseSent
	}
	weekday, ok := movieclub.ParseWeekday(fields[1])
	if !ok {
		h.reply(ctx, chatID, "День недели: mon..sun или пн..вс.")
		return errResponseSent
	}
	return h.MovieClub.SetSchedule(ctx, feature, operationID, chatID, weekday, fields[2], true)
}

func (h *Handler) pauseMovieSchedule(ctx context.Context, operationID, chatID int64, args string) error {
	fields := strings.Fields(args)
	if len(fields) < 1 || len(fields) > 2 {
		h.reply(ctx, chatID, "Формат: /movie_pause genre|reference или /movie_pause genre|reference wed.")
		return errResponseSent
	}
	feature := movieclub.Feature(fields[0])
	if !feature.Valid() {
		h.reply(ctx, chatID, "Тип опроса: genre или reference.")
		return errResponseSent
	}
	if len(fields) == 1 {
		return h.MovieClub.PauseSchedules(ctx, feature, operationID, chatID)
	}
	weekday, ok := movieclub.ParseWeekday(fields[1])
	if !ok {
		h.reply(ctx, chatID, "День недели: mon..sun или пн..вс.")
		return errResponseSent
	}
	return h.MovieClub.SetSchedule(ctx, feature, operationID, chatID, weekday, "00:00", false)
}

func (h *Handler) resolveMovieRound(ctx context.Context, operationID, chatID int64, args string) error {
	fields := strings.Fields(args)
	if len(fields) != 2 || (fields[1] != "sent" && fields[1] != "retry" && fields[1] != "cancel") {
		h.reply(ctx, chatID, "Формат: /movie_resolve ID sent|retry|cancel.")
		return errResponseSent
	}
	roundID, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return err
	}
	action := movieclub.ResolveAction(fields[1])
	if err = h.MovieClub.Resolve(ctx, operationID, chatID, roundID, action); err != nil {
		return err
	}
	switch action {
	case movieclub.ResolveRetry:
		h.reply(ctx, chatID, fmt.Sprintf("Опрос или подборка #%d возвращены в очередь. Если это пропущенный опрос, он откроется на 24 часа.", roundID))
	case movieclub.ResolveSent:
		h.reply(ctx, chatID, fmt.Sprintf("Подборка #%d отмечена как доставленная.", roundID))
	case movieclub.ResolveCancel:
		h.reply(ctx, chatID, fmt.Sprintf("Подборка #%d отменена.", roundID))
	}
	return errResponseSent
}

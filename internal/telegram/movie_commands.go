package telegram

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

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
		return true, h.startGenrePoll(ctx, update.ID, chatID, args)
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
	default:
		return false, nil
	}
}

func isMovieCommand(command string) bool {
	switch command {
	case "/genre_poll", "/movie_schedule", "/movie_pause", "/movie_settings", "/movie_resolve":
		return true
	default:
		return false
	}
}

func (h *Handler) startGenrePoll(ctx context.Context, operationID, chatID int64, args string) error {
	duration := 24 * time.Hour
	if args != "" {
		parsed, err := time.ParseDuration(args)
		if err != nil {
			h.reply(ctx, chatID, "Формат: /genre_poll 10m. Допустимо от 5m до 24h.")
			return errResponseSent
		}
		duration = parsed
	}
	roundID, err := h.MovieClub.Start(ctx, operationID, chatID, duration)
	if err == nil {
		h.reply(ctx, chatID, fmt.Sprintf("Киноопрос #%d запланирован на %s.", roundID, duration))
		return errResponseSent
	}
	return err
}

func (h *Handler) setMovieSchedule(ctx context.Context, operationID, chatID int64, args string) error {
	fields := strings.Fields(args)
	if len(fields) != 3 || fields[0] != string(movieclub.Genre) {
		h.reply(ctx, chatID, "Формат: /movie_schedule genre wed 19:00. Сначала задайте /timezone.")
		return errResponseSent
	}
	weekday, ok := movieclub.ParseWeekday(fields[1])
	if !ok {
		h.reply(ctx, chatID, "День недели: mon..sun или пн..вс.")
		return errResponseSent
	}
	return h.MovieClub.SetSchedule(ctx, operationID, chatID, weekday, fields[2], true)
}

func (h *Handler) pauseMovieSchedule(ctx context.Context, operationID, chatID int64, args string) error {
	fields := strings.Fields(args)
	if len(fields) < 1 || len(fields) > 2 || fields[0] != string(movieclub.Genre) {
		h.reply(ctx, chatID, "Формат: /movie_pause genre или /movie_pause genre wed.")
		return errResponseSent
	}
	if len(fields) == 1 {
		return h.MovieClub.PauseSchedules(ctx, operationID, chatID)
	}
	weekday, ok := movieclub.ParseWeekday(fields[1])
	if !ok {
		h.reply(ctx, chatID, "День недели: mon..sun или пн..вс.")
		return errResponseSent
	}
	return h.MovieClub.SetSchedule(ctx, operationID, chatID, weekday, "00:00", false)
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
	return h.MovieClub.Resolve(ctx, operationID, chatID, roundID, movieclub.ResolveAction(fields[1]))
}

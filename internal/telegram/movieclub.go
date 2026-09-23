package telegram

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/vaporon4a/movie-helper/internal/movieclub"
)

type MovieSender struct {
	api       API
	posterURL func(string) string
}

func NewMovieSender(api API, posterURL func(string) string) (MovieSender, error) {
	if api == nil || posterURL == nil {
		return MovieSender{}, errMissingDependency
	}
	return MovieSender{api: api, posterURL: posterURL}, nil
}

func (s MovieSender) OpenPoll(ctx context.Context, chat int64, feature movieclub.Feature, labels []string) (string, int, error) {
	options := make([]models.InputPollOption, len(labels))
	for i, label := range labels {
		options[i] = models.InputPollOption{Text: label}
	}
	anonymous := true
	question, description := moviePollCopy(feature)
	message, err := s.api.SendPoll(ctx, &bot.SendPollParams{
		ChatID: chat, Question: question, Options: options,
		IsAnonymous: &anonymous, Type: "regular", AllowsRevoting: true,
		Description: description,
	})
	if err != nil {
		return "", 0, classifyMovie(err)
	}
	if message == nil || message.Poll == nil || message.Poll.ID == "" {
		return "", 0, &movieclub.DeliveryError{Kind: movieclub.DeliveryUnknown}
	}
	return message.Poll.ID, message.ID, nil
}

func moviePollCopy(feature movieclub.Feature) (string, string) {
	if feature == movieclub.Reference {
		return "На какой известный фильм ориентируемся?",
			"После завершения бот пришлёт похожие фильмы и работы авторов победившего варианта."
	}
	return "Какой жанр выбираем для следующего киновечера?",
		"После завершения бот пришлёт до 20 популярных фильмов победившего жанра."
}

func (s MovieSender) ClosePoll(ctx context.Context, chat int64, messageID int) ([]int, error) {
	poll, err := s.api.StopPoll(ctx, &bot.StopPollParams{ChatID: chat, MessageID: messageID})
	if err != nil {
		return nil, classifyMovie(err)
	}
	if poll == nil {
		return nil, &movieclub.DeliveryError{Kind: movieclub.DeliveryUnknown}
	}
	votes := make([]int, len(poll.Options))
	for i, option := range poll.Options {
		votes[i] = option.VoterCount
	}
	return votes, nil
}

func (s MovieSender) SendSummary(ctx context.Context, chat int64, summary movieclub.Summary, roundID int64, more bool) (int, error) {
	params := &bot.SendMessageParams{ChatID: chat, Text: selectionSummary(summary)}
	if more {
		params.ReplyMarkup = &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{{
			Text: "Ещё 10 фильмов", CallbackData: fmt.Sprintf("movie_more:%d", roundID),
		}}}}
	}
	message, err := s.api.SendMessage(ctx, params)
	if err != nil {
		return 0, classifyMovie(err)
	}
	if message == nil {
		return 0, &movieclub.DeliveryError{Kind: movieclub.DeliveryUnknown}
	}
	return message.ID, nil
}

func (s MovieSender) SendMovies(ctx context.Context, chat int64, movies []movieclub.Recommendation) ([]int, error) {
	media := s.movieMedia(movies)
	switch len(media) {
	case 0:
		return nil, nil
	case 1:
		return s.sendMoviePhoto(ctx, chat, media[0].(*models.InputMediaPhoto))
	default:
		return s.sendMovieAlbum(ctx, chat, media)
	}
}

func (s MovieSender) movieMedia(movies []movieclub.Recommendation) []models.InputMedia {
	media := make([]models.InputMedia, 0, len(movies))
	for _, movie := range movies {
		poster := s.posterURL(movie.PosterPath)
		if poster != "" {
			media = append(media, &models.InputMediaPhoto{Media: poster, Caption: movieCaption(movie)})
		}
	}
	return media
}

func (s MovieSender) sendMoviePhoto(ctx context.Context, chat int64, photo *models.InputMediaPhoto) ([]int, error) {
	message, err := s.api.SendPhoto(ctx, &bot.SendPhotoParams{ChatID: chat, Photo: &models.InputFileString{Data: photo.Media}, Caption: photo.Caption})
	if err != nil {
		return nil, classifyMovie(err)
	}
	if message == nil {
		return nil, &movieclub.DeliveryError{Kind: movieclub.DeliveryUnknown}
	}
	return []int{message.ID}, nil
}

func (s MovieSender) sendMovieAlbum(ctx context.Context, chat int64, media []models.InputMedia) ([]int, error) {
	messages, err := s.api.SendMediaGroup(ctx, &bot.SendMediaGroupParams{ChatID: chat, Media: media})
	if err != nil {
		return nil, classifyMovie(err)
	}
	if len(messages) != len(media) {
		return nil, &movieclub.DeliveryError{Kind: movieclub.DeliveryUnknown}
	}
	ids := make([]int, len(messages))
	for i, message := range messages {
		if message == nil {
			return nil, &movieclub.DeliveryError{Kind: movieclub.DeliveryUnknown}
		}
		ids[i] = message.ID
	}
	return ids, nil
}

func selectionSummary(summary movieclub.Summary) string {
	if summary.NoVotes {
		return "Опрос завершён без голосов — подборки сегодня не будет."
	}
	title := "🎬 Победил жанр: "
	if summary.Feature == movieclub.Reference {
		title = "🎬 Фильм-ориентир: "
	}
	lines := []string{title + summary.Winner, ""}
	for i, movie := range summary.Movies {
		if i == 10 {
			break
		}
		year := ""
		if movie.Year != 0 {
			year = fmt.Sprintf(" (%d)", movie.Year)
		}
		lines = append(lines, fmt.Sprintf("%d. %s%s — %.1f", i+1, movie.Title, year, movie.Rating))
	}
	if len(summary.Movies) == 0 {
		lines = append(lines, "TMDB не вернул подходящих фильмов.")
	}
	lines = append(lines, "", "Данные и изображения: TMDB. This product uses the TMDB API but is not endorsed or certified by TMDB.")
	return strings.Join(lines, "\n")
}

func movieSettingsText(settings movieclub.SettingsView) string {
	lines := []string{"Киноопросы по жанрам:"}
	if len(settings.Schedules) == 0 {
		lines = append(lines, "расписание выключено")
	}
	for _, schedule := range settings.Schedules {
		state := "выключено"
		if schedule.Enabled {
			state = "включено"
		}
		lines = append(lines, fmt.Sprintf("%s %s — %s", movieclub.WeekdayName(schedule.Weekday), schedule.Clock, state))
	}
	for _, round := range settings.Rounds {
		if round.State == movieclub.StatePublished || round.State == movieclub.StateCancelled || round.State == movieclub.StateFailed {
			continue
		}
		line := fmt.Sprintf("Раунд #%d: %s", round.ID, round.State)
		if round.State == movieclub.StateOpen {
			line += ", закрытие " + time.Unix(round.ClosesAt, 0).Format(time.RFC3339)
		}
		if round.ErrorCode != "" {
			line += ", ошибка " + round.ErrorCode
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func movieCaption(movie movieclub.Recommendation) string {
	year := ""
	if movie.Year != 0 {
		year = fmt.Sprintf(" (%d)", movie.Year)
	}
	parts := []string{fmt.Sprintf("%s%s · %.1f/10", movie.Title, year, movie.Rating)}
	if movie.Overview != "" {
		parts = append(parts, movie.Overview)
	}
	parts = append(parts, fmt.Sprintf("https://www.themoviedb.org/movie/%d", movie.ID))
	return strings.Join(parts, "\n")
}

func classifyMovie(err error) error {
	var migrate *bot.MigrateError
	if rate, ok := errors.AsType[*bot.TooManyRequestsError](err); ok {
		return &movieclub.DeliveryError{Kind: movieclub.DeliveryRetry, After: time.Duration(rate.RetryAfter) * time.Second}
	}
	if errors.Is(err, bot.ErrorForbidden) || errors.Is(err, bot.ErrorUnauthorized) || errors.As(err, &migrate) {
		return &movieclub.DeliveryError{Kind: movieclub.DeliveryForbidden}
	}
	if errors.Is(err, bot.ErrorBadRequest) || errors.Is(err, bot.ErrorNotFound) {
		return &movieclub.DeliveryError{Kind: movieclub.DeliveryPermanent}
	}
	return &movieclub.DeliveryError{Kind: movieclub.DeliveryUnknown}
}

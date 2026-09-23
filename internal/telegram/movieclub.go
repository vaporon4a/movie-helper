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

type MovieSender struct{ API API }

func (s MovieSender) OpenPoll(ctx context.Context, chat int64, labels []string, closes time.Time) (string, int, error) {
	options := make([]models.InputPollOption, len(labels))
	for i, label := range labels {
		options[i] = models.InputPollOption{Text: label}
	}
	anonymous := true
	closeDate := 0
	untilClose := time.Until(closes)
	// Telegram accepts automatic poll closing only 5-600 seconds ahead.
	// Longer movieclub polls are closed by the persistent scheduler.
	if untilClose >= 5*time.Second && untilClose <= 10*time.Minute {
		closeDate = int(closes.Unix())
	}
	message, err := s.API.SendPoll(ctx, &bot.SendPollParams{
		ChatID: chat, Question: "Какой жанр выбираем для следующего киновечера?", Options: options,
		IsAnonymous: &anonymous, Type: "regular", AllowsRevoting: true, CloseDate: closeDate,
		Description: "После завершения бот пришлёт до 20 популярных фильмов победившего жанра.",
	})
	if err != nil {
		return "", 0, classifyMovie(err)
	}
	if message == nil || message.Poll == nil || message.Poll.ID == "" {
		return "", 0, &movieclub.DeliveryError{Kind: movieclub.DeliveryUnknown}
	}
	return message.Poll.ID, message.ID, nil
}

func (s MovieSender) ClosePoll(ctx context.Context, chat int64, messageID int) ([]int, error) {
	poll, err := s.API.StopPoll(ctx, &bot.StopPollParams{ChatID: chat, MessageID: messageID})
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

func (s MovieSender) SendSummary(ctx context.Context, chat int64, text string, roundID int64, more bool) (int, error) {
	params := &bot.SendMessageParams{ChatID: chat, Text: text}
	if more {
		params.ReplyMarkup = &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{{
			Text: "Ещё 10 фильмов", CallbackData: fmt.Sprintf("movie_more:%d", roundID),
		}}}}
	}
	message, err := s.API.SendMessage(ctx, params)
	if err != nil {
		return 0, classifyMovie(err)
	}
	if message == nil {
		return 0, &movieclub.DeliveryError{Kind: movieclub.DeliveryUnknown}
	}
	return message.ID, nil
}

func (s MovieSender) SendMovies(ctx context.Context, chat int64, movies []movieclub.Recommendation, catalog movieclub.Catalog) ([]int, error) {
	media := make([]models.InputMedia, 0, len(movies))
	for _, movie := range movies {
		poster := catalog.PosterURL(movie.PosterPath)
		if poster != "" {
			media = append(media, &models.InputMediaPhoto{Media: poster, Caption: movieCaption(movie)})
		}
	}
	if len(media) == 0 {
		return nil, nil
	}
	if len(media) == 1 {
		photo := media[0].(*models.InputMediaPhoto)
		message, err := s.API.SendPhoto(ctx, &bot.SendPhotoParams{ChatID: chat, Photo: &models.InputFileString{Data: photo.Media}, Caption: photo.Caption})
		if err != nil {
			return nil, classifyMovie(err)
		}
		if message == nil {
			return nil, &movieclub.DeliveryError{Kind: movieclub.DeliveryUnknown}
		}
		return []int{message.ID}, nil
	}
	messages, err := s.API.SendMediaGroup(ctx, &bot.SendMediaGroupParams{ChatID: chat, Media: media})
	if err != nil {
		return nil, classifyMovie(err)
	}
	if len(messages) != len(media) {
		return nil, &movieclub.DeliveryError{Kind: "unknown"}
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

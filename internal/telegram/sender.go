package telegram

import (
	"context"
	"errors"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/vaporon4a/movie-helper/internal/daily"
)

type Sender struct{ API API }

func (s Sender) Send(ctx context.Context, chatID int64, item daily.Item) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var message *models.Message
	var err error
	if item.Kind == daily.Meme {
		text := "Мем дня\n" + item.Text
		if item.Source != "" {
			text += "\nИсточник: " + item.Source
		}
		message, err = s.API.SendPhoto(ctx, &bot.SendPhotoParams{ChatID: chatID, Photo: &models.InputFileString{Data: item.Image}, Caption: text})
	} else {
		message, err = s.API.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "Факт о кино\n" + item.Text + "\nИсточник: " + item.Source})
	}
	if err != nil {
		return 0, classify(err)
	}
	if message == nil {
		return 0, &daily.SendError{Kind: "unknown"}
	}
	return message.ID, nil
}

func classify(err error) error {
	var migrate *bot.MigrateError
	if rate, ok := errors.AsType[*bot.TooManyRequestsError](err); ok {
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

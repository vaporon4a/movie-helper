package telegram

import (
	"context"
	"errors"
	"html"
	"log/slog"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/vaporon4a/movie-helper/internal/daily"
	"github.com/vaporon4a/movie-helper/internal/feedback"
)

type PostRecorder interface {
	RegisterFeedbackPost(context.Context, feedback.Post) error
}

type Sender struct {
	API         API
	Feedback    PostRecorder
	Publication string
	Log         *slog.Logger
	Now         func() time.Time
}

func (s Sender) Send(ctx context.Context, chatID int64, item daily.Item) (int, error) {
	sendCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var message *models.Message
	var err error
	if item.Kind == daily.Meme {
		message, err = s.API.SendPhoto(sendCtx, &bot.SendPhotoParams{
			ChatID: chatID, Photo: &models.InputFileString{Data: item.Image},
			Caption: memeCaption(item), ParseMode: models.ParseModeHTML,
		})
	} else {
		message, err = s.API.SendMessage(sendCtx, &bot.SendMessageParams{
			ChatID: chatID, Text: factMessage(item), ParseMode: models.ParseModeHTML,
			LinkPreviewOptions: disabledLinkPreview(),
		})
	}
	if err != nil {
		return 0, classify(err)
	}
	if message == nil || message.ID <= 0 {
		return 0, &daily.SendError{Kind: "unknown"}
	}
	s.registerFeedback(ctx, chatID, message.ID, item)
	return message.ID, nil
}

func (s Sender) registerFeedback(ctx context.Context, chat int64, message int, item daily.Item) {
	if s.Feedback == nil {
		return
	}
	now, log := s.Now, s.Log
	if now == nil {
		now = time.Now
	}
	if log == nil {
		log = slog.Default()
	}
	publication := s.Publication
	if publication == "" {
		publication = feedback.Scheduled
	}
	err := s.Feedback.RegisterFeedbackPost(ctx, feedback.Post{ChatID: chat, MessageID: message,
		Publication: publication, PublishedAt: now(), Item: item})
	if err != nil {
		// The Telegram send is confirmed. A statistics failure must never resend it.
		log.Warn("publication feedback registration failed", "chat_id", chat, "message_id", message, "kind", item.Kind)
	}
}

func memeCaption(item daily.Item) string {
	const previewPrefix = "Предпросмотр · "
	title := strings.TrimSpace(item.Text)
	heading := "🎭 <b>Мем дня</b>"
	if strings.HasPrefix(title, previewPrefix) {
		heading += " · <i>предпросмотр</i>"
		title = strings.TrimSpace(strings.TrimPrefix(title, previewPrefix))
	}
	parts := []string{heading}
	if title != "" {
		parts = append(parts, html.EscapeString(title))
	}
	if item.Source != "" {
		parts = append(parts, `🔗 <a href="`+html.EscapeString(item.Source)+`">Источник</a>`)
	}
	return strings.Join(parts, "\n\n")
}

func factMessage(item daily.Item) string {
	const previewPrefix = "Предпросмотр · "
	text := strings.TrimSpace(item.Text)
	heading := "🎬 <b>Факт о кино</b>"
	if strings.HasPrefix(text, previewPrefix) {
		heading += " · <i>предпросмотр</i>"
		text = strings.TrimSpace(strings.TrimPrefix(text, previewPrefix))
	}
	body, wikipedia := strings.CutSuffix(text, "\n\n"+daily.WikipediaAttribution)
	extracted, original := strings.CutSuffix(text, "\n\n"+daily.WikipediaExtractiveAttribution)
	if original {
		body = extracted
		wikipedia = true
	}
	if wikipedia {
		text = strings.TrimSpace(body)
	}
	parts := []string{heading}
	if text != "" {
		if original {
			title, story, _ := strings.Cut(text, "\n\n")
			parts = append(parts, "<b>"+html.EscapeString(title)+"</b>\n\n"+html.EscapeString(story))
		} else {
			parts = append(parts, html.EscapeString(text))
		}
	}
	if wikipedia {
		attributionNote := "Текст переработан AI"
		if original {
			attributionNote = "Исходный русский текст · отобрано AI"
		}
		parts = append(parts, `📚 <a href="`+html.EscapeString(item.Source)+`">Wikipedia</a> · <a href="`+daily.WikipediaLicenseURL+`">CC BY-SA 4.0</a>`+"\n<i>"+attributionNote+"</i>")
	} else if item.Source != "" {
		parts = append(parts, `🔗 <a href="`+html.EscapeString(item.Source)+`">Источник</a>`)
	}
	return strings.Join(parts, "\n\n")
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

func disabledLinkPreview() *models.LinkPreviewOptions {
	return &models.LinkPreviewOptions{IsDisabled: new(true)}
}

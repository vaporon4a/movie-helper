package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/go-telegram/bot"
	"github.com/vaporon4a/movie-helper/internal/telegram"
)

// Menu registration is cosmetic: a temporary Telegram failure must not stop
// scheduled posts, poll collection or processing commands written by hand.
func configureTelegramMenu(ctx context.Context, b *bot.Bot, log *slog.Logger) {
	for _, language := range []string{"", "ru"} {
		request, cancel := context.WithTimeout(ctx, 8*time.Second)
		ok, err := b.SetMyCommands(request, &bot.SetMyCommandsParams{Commands: telegram.CoreCommands(), LanguageCode: language})
		cancel()
		if err != nil || !ok {
			log.WarnContext(ctx, "telegram command menu not updated", "language", language, "reason", "telegram_request_failed")
			continue
		}
		log.InfoContext(ctx, "telegram command menu updated", "language", language, "commands", len(telegram.CoreCommands()))
	}
}

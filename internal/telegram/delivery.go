package telegram

import (
	"errors"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/vaporon4a/movie-helper/internal/clubprofile"
)

func classifyProfileDelivery(err error) *clubprofile.DeliveryError {
	if rate, ok := errors.AsType[*bot.TooManyRequestsError](err); ok {
		return &clubprofile.DeliveryError{Kind: "retry", After: time.Duration(rate.RetryAfter) * time.Second, Reason: "rate_limit"}
	}
	var migrate *bot.MigrateError
	if errors.Is(err, bot.ErrorForbidden) || errors.Is(err, bot.ErrorUnauthorized) || errors.As(err, &migrate) {
		return &clubprofile.DeliveryError{Kind: "forbidden", Reason: "forbidden"}
	}
	if errors.Is(err, bot.ErrorBadRequest) || errors.Is(err, bot.ErrorNotFound) {
		return &clubprofile.DeliveryError{Kind: "permanent", Reason: telegramRejectionReason(err)}
	}
	return &clubprofile.DeliveryError{Kind: clubprofile.DeliveryUnknown, Reason: "transport_unknown"}
}

func telegramRejectionReason(err error) string {
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "failed to get http url content"):
		return "photo_url_fetch"
	case strings.Contains(message, "wrong type of the web page content"):
		return "photo_url_content_type"
	case strings.Contains(message, "can't parse entities"):
		return "invalid_html"
	case strings.Contains(message, "caption is too long"):
		return "caption_too_long"
	case strings.Contains(message, "message is too long"):
		return "message_too_long"
	case strings.Contains(message, "object expected as reply markup"):
		return "invalid_reply_markup"
	case errors.Is(err, bot.ErrorNotFound):
		return "not_found"
	default:
		return "bad_request"
	}
}

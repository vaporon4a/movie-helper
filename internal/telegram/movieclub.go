package telegram

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/vaporon4a/movie-helper/internal/movieclub"
)

type MovieSender struct {
	api       API
	posterURL func(string) string
	log       *slog.Logger
}

func NewMovieSender(api API, posterURL func(string) string, log *slog.Logger) (MovieSender, error) {
	if api == nil || posterURL == nil {
		return MovieSender{}, errMissingDependency
	}
	if log == nil {
		log = slog.Default()
	}
	return MovieSender{api: api, posterURL: posterURL, log: log}, nil
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
		classified := classifyMovie(err)
		s.log.Warn("telegram movie delivery failed", "operation", "open_poll", "chat_id", chat, "feature", feature, "options", len(labels), "reason", deliveryReason(classified))
		return "", 0, classified
	}
	if message == nil || message.Poll == nil || message.Poll.ID == "" {
		return "", 0, &movieclub.DeliveryError{Kind: movieclub.DeliveryUnknown}
	}
	return message.Poll.ID, message.ID, nil
}

func moviePollCopy(feature movieclub.Feature) (string, string) {
	if feature == movieclub.Reference {
		return "Какой фильм взять за ориентир для следующей подборки?",
			"Выберите фильм по настроению: после голосования бот найдёт похожие картины и другие работы его создателей."
	}
	return "Какой жанр выбираем для следующего киновечера?",
		"После завершения бот пришлёт до 20 популярных фильмов победившего жанра."
}

func (s MovieSender) ClosePoll(ctx context.Context, chat int64, messageID int) ([]int, error) {
	poll, err := s.api.StopPoll(ctx, &bot.StopPollParams{ChatID: chat, MessageID: messageID})
	if err != nil {
		classified := classifyMovie(err)
		s.log.Warn("telegram movie delivery failed", "operation", "close_poll", "chat_id", chat, "reason", deliveryReason(classified))
		return nil, classified
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
	var markup models.ReplyMarkup
	if more {
		markup = moreMoviesKeyboard(roundID)
	}
	poster := ""
	if summary.Hero.PosterPath != "" {
		poster = s.posterURL(summary.Hero.PosterPath)
	}
	if summary.Feature == movieclub.Reference && poster != "" {
		caption := referenceSummaryPlain(summary, 1024)
		message, err := s.api.SendPhoto(ctx, &bot.SendPhotoParams{
			ChatID: chat, Photo: &models.InputFileString{Data: poster}, Caption: caption,
			ReplyMarkup: markup,
		})
		if err == nil && message != nil {
			return message.ID, nil
		}
		if err == nil {
			return 0, &movieclub.DeliveryError{Kind: movieclub.DeliveryUnknown}
		}
		classified := classifyMovie(err)
		s.log.Warn("telegram movie delivery failed", "operation", "winner_poster", "round_id", roundID, "chat_id", chat,
			"reason", deliveryReason(classified), "caption_runes", utf8.RuneCountInString(caption))
		typed, permanent := errors.AsType[*movieclub.DeliveryError](classified)
		if !permanent || typed.Kind != movieclub.DeliveryPermanent {
			return 0, classified
		}
		s.log.Info("telegram movie delivery fallback", "operation", "winner_summary_text", "round_id", roundID, "chat_id", chat)
	}
	text := selectionSummary(summary, more)
	parseMode := models.ParseModeHTML
	if summary.Feature == movieclub.Reference {
		text = referenceSummaryPlain(summary, 4096)
		parseMode = ""
	}
	params := &bot.SendMessageParams{
		ChatID: chat, Text: text, ParseMode: parseMode,
		LinkPreviewOptions: disabledLinkPreview(),
		ReplyMarkup:        markup,
	}
	message, err := s.api.SendMessage(ctx, params)
	if err != nil {
		classified := classifyMovie(err)
		s.log.Warn("telegram movie delivery failed", "operation", "summary_text", "round_id", roundID, "chat_id", chat,
			"reason", deliveryReason(classified), "text_runes", utf8.RuneCountInString(params.Text))
		return 0, classified
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
		classified := classifyMovie(err)
		s.log.Warn("telegram movie delivery failed", "operation", "recommendation_photo", "chat_id", chat,
			"reason", deliveryReason(classified), "caption_runes", utf8.RuneCountInString(photo.Caption))
		return nil, classified
	}
	if message == nil {
		return nil, &movieclub.DeliveryError{Kind: movieclub.DeliveryUnknown}
	}
	return []int{message.ID}, nil
}

func (s MovieSender) sendMovieAlbum(ctx context.Context, chat int64, media []models.InputMedia) ([]int, error) {
	messages, err := s.api.SendMediaGroup(ctx, &bot.SendMediaGroupParams{ChatID: chat, Media: media})
	if err != nil {
		classified := classifyMovie(err)
		s.log.Warn("telegram movie delivery failed", "operation", "recommendation_album", "chat_id", chat,
			"reason", deliveryReason(classified), "items", len(media), "max_caption_runes", maxMediaCaptionRunes(media))
		return nil, classified
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

func selectionSummaryEdit(chat int64, messageID int, summary movieclub.Summary) *bot.EditMessageTextParams {
	return &bot.EditMessageTextParams{
		ChatID: chat, MessageID: messageID, Text: selectionSummary(summary, false), ParseMode: models.ParseModeHTML,
		LinkPreviewOptions: disabledLinkPreview(),
		ReplyMarkup:        &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{}},
	}
}

func disabledLinkPreview() *models.LinkPreviewOptions {
	disabled := true
	return &models.LinkPreviewOptions{IsDisabled: &disabled}
}

func moreMoviesKeyboard(roundID int64) *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{{
		Text: "Ещё 10 фильмов", CallbackData: fmt.Sprintf("movie_more:%d", roundID),
	}}}}
}

func selectionSummary(summary movieclub.Summary, more bool) string {
	return selectionSummaryWithin(summary, more, 4096)
}

func selectionSummaryWithin(summary movieclub.Summary, more bool, limit int) string {
	for _, titleRunes := range []int{80, 60, 40, 24, 16} {
		text := selectionSummaryWithTitleLimit(summary, more, titleRunes)
		if utf8.RuneCountInString(text) <= limit {
			return text
		}
	}
	return selectionSummaryWithTitleLimit(summary, more, 8)
}

func selectionSummaryWithTitleLimit(summary movieclub.Summary, more bool, titleRunes int) string {
	if summary.NoVotes {
		return "Опрос завершён без голосов — подборки сегодня не будет."
	}
	winner := html.EscapeString(summary.Winner)
	title := "🏆 <b>Победил жанр: " + winner + "</b>"
	if summary.Feature == movieclub.Reference {
		title = "🎬 <b>Фильм-ориентир: " + winner + "</b>"
	}
	total := max(summary.Total, len(summary.Movies))
	count := fmt.Sprintf("🎞 %d фильмов разных эпох", total)
	if summary.Feature == movieclub.Reference {
		count = fmt.Sprintf("✨ %d рекомендаций по результатам выбора", total)
	}
	if more {
		count = fmt.Sprintf("🎞 %d из %d фильмов", len(summary.Movies), total)
	}
	lines := []string{title, count, ""}
	lines = appendMovieSummaryLines(lines, summary, titleRunes)
	if len(summary.Movies) == 0 {
		lines = append(lines, "TMDB не вернул подходящих фильмов.")
	}
	if more {
		lines = append(lines, "", "Нажмите «Ещё 10 фильмов», чтобы открыть продолжение.")
	}
	lines = append(lines, "", `<i>Источник данных и постеров: <a href="https://www.themoviedb.org/">TMDB</a>`,
		"This product uses the TMDB API but is not endorsed or certified by TMDB.</i>")
	return strings.Join(lines, "\n")
}

func appendMovieSummaryLines(lines []string, summary movieclub.Summary, titleRunes int) []string {
	lastRelation := ""
	position := 0
	for i, movie := range summary.Movies {
		if summary.Feature == movieclub.Reference && movie.Relation != lastRelation {
			if lastRelation != "" {
				lines = append(lines, "")
			}
			lines = append(lines, referenceRelationTitle(movie.Relation))
			lastRelation = movie.Relation
			position = 0
		}
		position++
		number := i + 1
		if summary.Feature == movieclub.Reference {
			number = position
		}
		parts := []string{fmt.Sprintf("%d. %s", number, movieSummaryTitle(movie, titleRunes))}
		if movie.Year != 0 {
			parts = append(parts, fmt.Sprintf("%d", movie.Year))
		}
		parts = append(parts, fmt.Sprintf("⭐ %.1f", movie.Rating))
		lines = append(lines, strings.Join(parts, " · "))
	}
	return lines
}

func referenceRelationTitle(relation string) string {
	switch relation {
	case "similar":
		return "🔎 <b>Похожи по настроению и жанру</b>"
	case "director":
		return "🎥 <b>Другие фильмы режиссёра</b>"
	case "screenwriter":
		return "✍️ <b>Работы сценариста</b>"
	case "book_author":
		return "📚 <b>Другие экранизации автора</b>"
	default:
		return "🎞 <b>Ещё фильмы</b>"
	}
}

func referenceSummaryPlain(summary movieclub.Summary, limit int) string {
	if summary.NoVotes {
		return "Опрос завершён без голосов — подборки сегодня не будет."
	}
	lines := []string{
		"🎬 Фильм-ориентир: " + summary.Winner,
		fmt.Sprintf("✨ %d рекомендаций по результатам выбора", max(summary.Total, len(summary.Movies))),
	}
	lastRelation, position := "", 0
	for _, movie := range summary.Movies {
		if movie.Relation != lastRelation {
			lines = append(lines, "", referenceRelationPlain(movie.Relation))
			lastRelation, position = movie.Relation, 0
		}
		position++
		title := movie.Title
		if title == "" {
			title = "Без названия"
		}
		line := fmt.Sprintf("%d. %s", position, title)
		if movie.Year != 0 {
			line += fmt.Sprintf(" · %d", movie.Year)
		}
		line += fmt.Sprintf(" · ⭐ %.1f", movie.Rating)
		lines = append(lines, line)
	}
	lines = append(lines, "", "Данные и постеры: TMDB")
	return truncateRunes(strings.Join(lines, "\n"), limit)
}

func referenceRelationPlain(relation string) string {
	switch relation {
	case "similar":
		return "🔎 Похожи по настроению и жанру"
	case "director":
		return "🎥 Другие фильмы режиссёра"
	case "screenwriter":
		return "✍️ Работы сценариста"
	case "book_author":
		return "📚 Другие экранизации автора"
	default:
		return "🎞 Ещё фильмы"
	}
}

func truncateRunes(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	if limit <= 1 {
		return "…"
	}
	return strings.TrimSpace(string(runes[:limit-1])) + "…"
}

func movieSummaryTitle(movie movieclub.Recommendation, limit int) string {
	title := movie.Title
	if title == "" {
		title = "Без названия"
	}
	runes := []rune(title)
	if len(runes) > limit {
		title = string(runes[:limit-1]) + "…"
	}
	title = html.EscapeString(title)
	if movie.ID <= 0 {
		return title
	}
	return fmt.Sprintf(`<a href="https://www.themoviedb.org/movie/%d">%s</a>`, movie.ID, title)
}

func movieSettingsText(settings movieclub.SettingsView) string {
	lines := []string{"🎬 Киноопросы:"}
	if len(settings.Schedules) == 0 {
		lines = append(lines, "расписание выключено")
	}
	for _, schedule := range settings.Schedules {
		state := "выключено"
		if schedule.Enabled {
			state = "включено"
		}
		kind := "Жанр"
		if schedule.Feature == movieclub.Reference {
			kind = "Фильм-ориентир"
		}
		lines = append(lines, fmt.Sprintf("%s · %s %s — %s", kind, movieclub.WeekdayName(schedule.Weekday), schedule.Clock, state))
	}
	for _, round := range settings.Rounds {
		lines = appendMovieRoundSettings(lines, round)
	}
	return strings.Join(lines, "\n")
}

func appendMovieRoundSettings(lines []string, round movieclub.Round) []string {
	if round.State == movieclub.StatePublished || round.State == movieclub.StateCancelled {
		return lines
	}
	line := fmt.Sprintf("Раунд #%d · %s: %s", round.ID, round.Feature, round.State)
	if round.State == movieclub.StateOpen {
		line += ", закрытие " + time.Unix(round.ClosesAt, 0).Format(time.RFC3339)
	}
	if round.ErrorCode != "" {
		line += ", ошибка " + round.ErrorCode
	}
	lines = append(lines, line)
	if round.State == movieclub.StateFailed && round.ErrorCode == "telegram_rejected" {
		lines = append(lines, fmt.Sprintf("Повторить сохранённую подборку: /movie_resolve %d retry", round.ID))
	}
	return lines
}

func movieCaption(movie movieclub.Recommendation) string {
	year := ""
	if movie.Year != 0 {
		year = fmt.Sprintf(" (%d)", movie.Year)
	}
	label := "🎬"
	if movie.Relation != "top" {
		label = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(referenceRelationTitle(movie.Relation), "<b>", ""), "</b>", ""))
	}
	parts := []string{label, fmt.Sprintf("%s%s · %.1f/10", movie.Title, year, movie.Rating)}
	if movie.Overview != "" {
		parts = append(parts, movie.Overview)
	}
	parts = append(parts, fmt.Sprintf("https://www.themoviedb.org/movie/%d", movie.ID))
	return strings.Join(parts, "\n")
}

func classifyMovie(err error) error {
	var migrate *bot.MigrateError
	if rate, ok := errors.AsType[*bot.TooManyRequestsError](err); ok {
		return &movieclub.DeliveryError{Kind: movieclub.DeliveryRetry, After: time.Duration(rate.RetryAfter) * time.Second, Reason: "rate_limit"}
	}
	if errors.Is(err, bot.ErrorForbidden) || errors.Is(err, bot.ErrorUnauthorized) || errors.As(err, &migrate) {
		return &movieclub.DeliveryError{Kind: movieclub.DeliveryForbidden, Reason: "forbidden"}
	}
	if errors.Is(err, bot.ErrorBadRequest) || errors.Is(err, bot.ErrorNotFound) {
		return &movieclub.DeliveryError{Kind: movieclub.DeliveryPermanent, Reason: telegramRejectionReason(err)}
	}
	return &movieclub.DeliveryError{Kind: movieclub.DeliveryUnknown, Reason: "transport_unknown"}
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

func deliveryReason(err error) string {
	if typed, ok := errors.AsType[*movieclub.DeliveryError](err); ok && typed.Reason != "" {
		return typed.Reason
	}
	return "unclassified"
}

func maxMediaCaptionRunes(media []models.InputMedia) int {
	maximum := 0
	for _, item := range media {
		if photo, ok := item.(*models.InputMediaPhoto); ok {
			maximum = max(maximum, utf8.RuneCountInString(photo.Caption))
		}
	}
	return maximum
}

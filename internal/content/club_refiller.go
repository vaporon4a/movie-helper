package content

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/vaporon4a/movie-helper/internal/ai"
	"github.com/vaporon4a/movie-helper/internal/aiwork"
	"github.com/vaporon4a/movie-helper/internal/clubreminder"
)

const reminderFallbackMode = "reminder_using_fallback"
const reminderPrimaryDeferred = "reminder_primary_deferred_"

type ClubRefillRepository interface {
	ClubSettings(context.Context, int64, time.Time) (clubreminder.Settings, error)
	ClubTexts(context.Context, int64, time.Time) ([]clubreminder.Text, error)
	SaveClubText(context.Context, int64, clubreminder.Text, time.Time) (bool, error)
}

type ClubRefiller struct {
	Store              ClubRefillRepository
	Primary, Secondary ai.Generator
}

func (r ClubRefiller) Execute(ctx context.Context, work aiwork.Work, now time.Time) (aiwork.Outcome, error) {
	settings, err := r.Store.ClubSettings(ctx, work.ScopeID, now)
	if err != nil {
		return aiwork.Outcome{}, err
	}
	if !settings.Enabled {
		return aiwork.Outcome{Done: true}, nil
	}
	texts, err := r.Store.ClubTexts(ctx, work.ScopeID, now)
	if err != nil {
		return aiwork.Outcome{}, err
	}
	intent, tone, full := clubRequest(texts)
	if full {
		return aiwork.Outcome{Done: true}, nil
	}
	if r.Primary == nil && r.Secondary == nil {
		return aiwork.Outcome{After: 6 * time.Hour, Reason: "provider_disabled"}, nil
	}
	deferrals := primaryDeferrals(work.LastErrorCode)
	secondaryOnly := r.Secondary != nil && (work.LastErrorCode == reminderFallbackMode || deferrals >= clubreminder.PrimaryDeferrals)
	text, err := r.generate(ctx, intent, tone, texts, secondaryOnly)
	if err != nil {
		return reminderFailure(err, now, deferrals, secondaryOnly), nil
	}
	saved, err := r.Store.SaveClubText(ctx, work.ScopeID, text, now)
	if err != nil {
		return aiwork.Outcome{}, err
	}
	reason := "stock_below_target"
	if !saved {
		reason = "duplicate_or_invalid_text"
	}
	if secondaryOnly {
		reason = reminderFallbackMode
	}
	return aiwork.Outcome{After: time.Minute, Reason: reason}, nil
}
func clubRequest(texts []clubreminder.Text) (string, string, bool) {
	stock, explore := 0, 0
	used := make(map[string]bool)
	for _, text := range texts {
		if text.State == "ready" {
			stock++
			used[text.Tone] = true
			if text.Intent == clubreminder.Explore {
				explore++
			}
		}
	}
	intent := clubreminder.Explore
	if explore >= 3 {
		intent = clubreminder.Discuss
	}
	tones := make([]string, 0, len(clubreminder.Tones))
	for _, tone := range clubreminder.Tones {
		if !used[tone] {
			tones = append(tones, tone)
		}
	}
	if len(tones) == 0 {
		tones = clubreminder.Tones
	}
	// #nosec G404 -- creative tone choice is not security-sensitive.
	tone := tones[rand.IntN(len(tones))]
	return intent, tone, stock >= clubreminder.Target
}
func (r ClubRefiller) generate(ctx context.Context, intent, tone string, history []clubreminder.Text, secondaryOnly bool) (clubreminder.Text, error) {
	instruction := fmt.Sprintf(`Напиши по-русски напоминание киноклубу о ручном выборе фильма. Цель %s: explore — предложить новые варианты, discuss — вернуться к обсуждению текущих желаний. Тон строго %s: friendly — дружеский, dramatic — театральный, sarcastic — сарказм, profane — грубый юмор с матом, roast — уничижительный стёб всего киноклуба за нерешительность с матом. Не делай личных нападок, не используй имена, угрозы или нападки на защищённые признаки. 100–180 слов, допустимо 60–240, максимум 2200 символов; 2–4 абзаца, до трёх emoji. Разные заходы, без повторения предыдущих текстов. В конце понятное приглашение принести название фильма или обсудить настроение. Без конкретных фильмов, дат, места, выдуманных событий и результатов голосования. Не обещай опросов. Без ссылок, @упоминаний, HTML или Markdown. Верни JSON index=0, text — готовое сообщение, evidence="".`, intent, tone)
	previous := make([]string, 0, len(history))
	for _, text := range history {
		previous = append(previous, text.Body)
	}
	parts := []ai.Part{{Text: "Ранее подготовленные сообщения бота, не повторяй их:\n" + strings.Join(previous, "\n---\n")}}
	var last error
	for index, provider := range []ai.Generator{r.Primary, r.Secondary} {
		if provider == nil || (index == 0 && secondaryOnly) {
			continue
		}
		attempt, cancel := context.WithTimeout(ctx, 45*time.Second)
		result, err := provider.Generate(attempt, instruction, parts)
		cancel()
		if err == nil {
			text := clubreminder.Text{Body: strings.TrimSpace(result.Text), Intent: intent, Tone: tone, Provider: []string{"gemini", "groq"}[index]}
			if result.Index != nil && *result.Index == 0 && result.Evidence == "" && clubreminder.ValidText(text) {
				return text, nil
			}
			err = &ai.ValidationError{Reason: "club_reminder_text"}
		}
		last = err
		if index == 0 && deferredProviderRetry(err) {
			return clubreminder.Text{}, &reminderPrimaryRetryError{cause: err}
		}
	}
	return clubreminder.Text{}, last
}

// This counter lives in the durable work reason, not in process memory.
func primaryDeferrals(reason string) int {
	for n := 1; n <= clubreminder.PrimaryDeferrals; n++ {
		if reason == fmt.Sprintf("%s%d", reminderPrimaryDeferred, n) {
			return n
		}
	}
	return 0
}

// Preserve the primary-provider origin while keeping safe metadata discoverable.
type reminderPrimaryRetryError struct{ cause error }

func (e *reminderPrimaryRetryError) Error() string { return e.cause.Error() }
func (e *reminderPrimaryRetryError) Unwrap() error { return e.cause }

func reminderFailure(err error, now time.Time, deferrals int, secondaryOnly bool) aiwork.Outcome {
	if errors.Is(err, ai.ErrDailyLimit) {
		return aiwork.Outcome{After: untilNextAIWindow(now), Reason: "local_daily_limit"}
	}
	after := 5 * time.Minute
	if g, ok := errors.AsType[interface {
		error
		RetryAfter() time.Duration
	}](err); ok && g.RetryAfter() > 0 {
		after = max(time.Second, min(g.RetryAfter(), 5*time.Minute))
	}
	reason := "reminder_generation_unavailable"
	if secondaryOnly {
		reason = reminderFallbackMode
	} else if _, ok := errors.AsType[*reminderPrimaryRetryError](err); ok {
		reason = fmt.Sprintf("%s%d", reminderPrimaryDeferred, min(deferrals+1, clubreminder.PrimaryDeferrals))
	}
	return aiwork.Outcome{After: after, Reason: reason}
}

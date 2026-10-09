package ai

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/vaporon4a/movie-helper/internal/daily"
)

// Version the scope when the selection policy changes. Only public-source
// rejections are shared across chats; chat publication history stays separate.
const MemeReviewVersion = "meme-v3"

const schemaTypeKey = "type"

const memeImageScope = "image:" + MemeReviewVersion

type Review struct {
	Interest, Clarity int
	Index             *int   `json:"index"`
	Reason            string `json:"reason"`
	Detail            string `json:"detail"`
}

type ReviewCache interface {
	MemeRejected(context.Context, string, string, time.Time) (bool, error)
	RejectMeme(context.Context, string, string, time.Time) error
}

const memeInstruction = `Ты редактор утренней рубрики для русскоязычного дружеского киноклуба. Оцени сами картинки, включая текст на них. Отметь accepted у каждого понятного без дополнительного контекста смешного мема, лучше о кино или повседневной жизни, и выбери лучший из них. Исключи рекламу, политическую агитацию, порнографию, жестокость, унижение групп людей, спойлеры и посты-вопросы без шутки. Не выбирай мем только за заголовок.
Верни JSON с полями index, text, evidence, reviews. index — номер лучшего принятого кандидата с нуля или -1, если ни один не подходит. text и evidence — пустые строки. reviews — объект с ОДНОЙ записью для КАЖДОГО кандидата: ключ — его номер строкой ("0", "1" и так далее), значение — объект с полями reason и detail. Не используй массив и не повторяй записи. reason: accepted (подходит), unreadable_text (текст картинки не удаётся прочитать), context_required (непонятно без внешнего контекста), not_meme (нет шутки), unsuitable (нарушает перечисленные ограничения), low_humor (понятно, но не смешно). detail — краткая конкретная причина по-русски, до 100 символов. У выбранного кандидата reason обязан быть accepted. Можно отметить accepted несколько кандидатов; index должен указывать на лучший. Если есть accepted, выбери одного из них; -1 допустим только когда все отклонены.`

func (c *Editor) SelectMeme(ctx context.Context, items []daily.Item) (*daily.Item, error) {
	items, err := c.SelectMemes(ctx, items, 1)
	if err != nil || len(items) == 0 {
		return nil, err
	}
	return &items[0], nil
}

func (c *Editor) SelectMemes(ctx context.Context, items []daily.Item, limit int) ([]daily.Item, error) {
	if c.MaxImages <= 0 {
		return nil, errors.New("invalid image batch size")
	}
	if limit <= 0 {
		return nil, errors.New("invalid approved meme limit")
	}
	session := c.memeSession()
	maxBatches := c.MaxBatches
	if maxBatches <= 0 {
		maxBatches = 2
	}
	approved := make([]daily.Item, 0, limit)
	next := 0
	for batch := 0; batch < maxBatches && next < len(items); batch++ {
		choices, parts, position, err := session.collect(ctx, items, next)
		next = position
		if err != nil {
			return partialMemes(approved, err)
		}
		if len(choices) == 0 {
			return approved, ctx.Err()
		}
		selected, stop, err := session.selectBatch(ctx, choices, parts, batch+1, approved)
		if err != nil || stop {
			return selected, err
		}
		approved = selected
		if len(approved) >= limit {
			return approved[:limit], nil
		}
	}
	return approved, nil
}
func partialMemes(approved []daily.Item, err error) ([]daily.Item, error) {
	if len(approved) > 0 && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
		return approved, nil
	}
	return nil, err
}

type memeReviewSession struct {
	editor *Editor
	now    func() time.Time
	log    *slog.Logger
}

func (c *Editor) memeSession() memeReviewSession {
	now, log := c.Now, c.Log
	if now == nil {
		now = time.Now
	}
	if log == nil {
		log = slog.Default()
	}
	return memeReviewSession{editor: c, now: now, log: log}
}
func (m memeReviewSession) collect(ctx context.Context, items []daily.Item, next int) ([]daily.Item, []Part, int, error) {
	choices := make([]daily.Item, 0, m.editor.MaxImages)
	parts := []Part{{Text: "Оцени пронумерованные мемы, отметь все подходящие и выбери лучший."}}
	for next < len(items) && len(choices) < m.editor.MaxImages {
		if err := ctx.Err(); err != nil {
			return nil, nil, next, err
		}
		item := items[next]
		next++
		media, err := m.candidate(ctx, item)
		if err != nil {
			return nil, nil, next, err
		}
		if media == nil {
			continue
		}
		parts = append(parts, Part{Text: fmt.Sprintf("Кандидат %d, заголовок: %s", len(choices), item.Text)}, Part{Inline: media})
		choices = append(choices, item)
	}
	return choices, parts, next, nil
}
func (m memeReviewSession) cached(ctx context.Context, item daily.Item) (bool, error) {
	if m.editor.Reviews == nil {
		return false, nil
	}
	unusable, err := m.editor.Reviews.MemeRejected(ctx, memeImageScope, item.Key, m.now())
	if err != nil {
		return false, errors.New("meme image cache unavailable")
	}
	if unusable {
		m.log.Info("meme image skipped", "scope", m.editor.Scope, "source", item.Key, "reason", "technical_cache")
		return true, nil
	}
	rejected, err := m.editor.Reviews.MemeRejected(ctx, m.editor.Scope, item.Key, m.now())
	if err != nil {
		return false, errors.New("meme review cache unavailable")
	}
	return rejected, nil
}
func (m memeReviewSession) candidate(ctx context.Context, item daily.Item) (*Inline, error) {
	skipped, err := m.cached(ctx, item)
	if err != nil || skipped {
		return nil, err
	}
	media, err := m.loadImage(ctx, item)
	if err == nil {
		return media, nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil, err
	}
	m.log.Info("meme image skipped", "scope", m.editor.Scope, "source", item.Key, "reason", imageFailureErrorCode(err))
	failure, _ := errors.AsType[*imageFailureError](err)
	if failure != nil && failure.cacheable && m.editor.Reviews != nil {
		if cacheErr := m.editor.Reviews.RejectMeme(ctx, memeImageScope, item.Key, m.now()); cacheErr != nil {
			return nil, errors.New("cannot save meme image rejection")
		}
	}
	return nil, nil
}
func (m memeReviewSession) loadImage(ctx context.Context, item daily.Item) (*Inline, error) {
	analysisURL := item.AnalysisImage
	if analysisURL == "" {
		analysisURL = item.Image
	}
	media, err := m.editor.image(ctx, analysisURL)
	if err != nil && item.AnalysisImage != "" && analysisURL != item.Image && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		m.log.Info("meme preview unavailable", "scope", m.editor.Scope, "source", item.Key, "reason", imageFailureErrorCode(err))
		return m.editor.image(ctx, item.Image)
	}
	return media, err
}
func (m memeReviewSession) saveReviews(ctx context.Context, result Selection, choices []daily.Item, batch int) error {
	for _, review := range result.Reviews {
		item := choices[*review.Index]
		m.log.Info("meme reviewed", "scope", m.editor.Scope, "source", item.Key, "batch", batch, "reason", review.Reason, "detail", review.Detail)
		if review.Reason != reviewAccepted && m.editor.Reviews != nil {
			if err := m.editor.Reviews.RejectMeme(ctx, m.editor.Scope, item.Key, m.now()); err != nil {
				return errors.New("cannot save meme rejection")
			}
		}
	}
	return nil
}
func acceptedMemes(result Selection, choices []daily.Item) []daily.Item {
	accepted := make([]daily.Item, 0, len(choices))
	if *result.Index >= 0 {
		accepted = append(accepted, choices[*result.Index])
	}
	for _, review := range result.Reviews {
		if review.Reason == reviewAccepted && *review.Index != *result.Index {
			accepted = append(accepted, choices[*review.Index])
		}
	}
	return accepted
}

func imageFailureErrorCode(err error) string {
	if failure, ok := errors.AsType[*imageFailureError](err); ok {
		return failure.code
	}
	return "image_processing_failed"
}

func validateReviews(s Selection, count int) error {
	if s.Index == nil || *s.Index < -1 || *s.Index >= count || len(s.Reviews) != count {
		return &ValidationError{Reason: "meme_review_count_or_index"}
	}
	seen := make(map[int]bool, count)
	accepted := make(map[int]bool, count)
	for _, r := range s.Reviews {
		if r.Index == nil || *r.Index < 0 || *r.Index >= count || seen[*r.Index] || strings.TrimSpace(r.Detail) == "" || utf8.RuneCountInString(r.Detail) > 160 {
			return &ValidationError{Reason: "meme_review_fields"}
		}
		seen[*r.Index] = true
		switch r.Reason {
		case reviewAccepted:
			accepted[*r.Index] = true
		case "unreadable_text", "context_required", "not_meme", "unsuitable", "low_humor":
		default:
			return &ValidationError{Reason: "meme_review_reason"}
		}
	}
	if (*s.Index == -1 && len(accepted) != 0) || (*s.Index >= 0 && !accepted[*s.Index]) {
		return &ValidationError{Reason: "meme_review_contradiction"}
	}
	return nil
}

// Facts retain their existing schema. Vision additionally requires a complete,
// validated per-image decision, so a bare index cannot silently approve a meme.
func SelectionSchema(parts []Part) map[string]any {
	for _, part := range parts {
		if part.FactCandidates > 0 {
			return FactSelectionSchema(part.FactCandidates)
		}
	}
	properties := map[string]any{"index": map[string]any{schemaTypeKey: schemaInteger}, schemaTextKey: map[string]any{schemaTypeKey: schemaString}, schemaEvidenceKey: map[string]any{schemaTypeKey: schemaString}}
	required := []string{"index", schemaTextKey, schemaEvidenceKey}
	count := 0
	for _, p := range parts {
		if p.Inline != nil {
			count++
		}
	}
	if count > 0 {
		reviews := map[string]any{}
		keys := make([]string, 0, count)
		indexes := []int{-1}
		for n := 0; n < count; n++ {
			key := fmt.Sprint(n)
			keys = append(keys, key)
			indexes = append(indexes, n)
			reviews[key] = map[string]any{
				schemaTypeKey: schemaObject, schemaAdditionalKey: false,
				schemaPropertiesKey: map[string]any{
					"reason": map[string]any{schemaTypeKey: schemaString, schemaEnumKey: []string{reviewAccepted, "unreadable_text", "context_required", "not_meme", "unsuitable", "low_humor"}},
					"detail": map[string]any{schemaTypeKey: schemaString},
				}, schemaRequiredKey: []string{"reason", "detail"},
			}
		}
		properties["index"] = map[string]any{schemaTypeKey: schemaInteger, schemaEnumKey: indexes}
		properties["reviews"] = map[string]any{schemaTypeKey: schemaObject, schemaAdditionalKey: false, schemaPropertiesKey: reviews, schemaRequiredKey: keys}
		required = append(required, "reviews")
	}
	return map[string]any{schemaTypeKey: schemaObject, schemaAdditionalKey: false, schemaPropertiesKey: properties, schemaRequiredKey: required}
}

func (m memeReviewSession) selectBatch(ctx context.Context, choices []daily.Item, parts []Part, batch int, approved []daily.Item) ([]daily.Item, bool, error) {
	result, err := m.editor.Generator.Generate(ctx, memeInstruction, parts)
	if err != nil {
		m.log.Warn("meme reserve refill failed", "scope", m.editor.Scope, "reason", "generator_failed")
		if len(approved) > 0 {
			return approved, true, nil
		}
		return nil, true, err
	}
	if err = validateReviews(result, len(choices)); err != nil {
		return nil, true, err
	}
	if err = m.saveReviews(ctx, result, choices, batch); err != nil {
		return nil, true, err
	}
	approved = append(approved, acceptedMemes(result, choices)...)
	return approved, false, nil
}

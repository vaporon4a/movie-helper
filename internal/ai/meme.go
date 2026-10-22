package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/vaporon4a/movie-helper/internal/daily"
)

// Version the scope when the selection policy changes. Only public-source
// rejections are shared across chats; chat publication history stays separate.
const MemeReviewVersion = daily.MemePolicy

const schemaTypeKey = "type"

const memeImageScope = "image:" + MemeReviewVersion

type Review struct {
	Interest, Clarity int
	Index             *int            `json:"index"`
	Reason            string          `json:"reason"`
	Detail            string          `json:"detail"`
	Meme              *MemeAssessment `json:"assessment,omitempty"`
}

type ReviewCache interface {
	MemeRejected(context.Context, string, string, time.Time) (bool, error)
	RejectMeme(context.Context, string, string, time.Time) error
	RejectMemeReview(context.Context, string, string, string, string, time.Time) error
}

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
	parts := []Part{{Text: "Опиши каждую картинку и её шутку, затем оцени качество по заданной шкале."}}
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

type memeAudit struct {
	Policy     string          `json:"policy"`
	Decision   string          `json:"decision"`
	Reason     string          `json:"reason"`
	Assessment *MemeAssessment `json:"assessment"`
}

func auditMeme(r Review) (string, error) {
	data, err := json.Marshal(memeAudit{Policy: MemeReviewVersion, Decision: MemeDecision(r), Reason: r.Reason, Assessment: r.Meme})
	if err != nil {
		return "", err
	}
	return string(data), nil
}
func (m memeReviewSession) saveReviews(ctx context.Context, result Selection, choices []daily.Item, batch int) error {
	for _, review := range result.Reviews {
		item := choices[*review.Index]
		if !validMemeExplanation(review) {
			m.log.Warn("meme explanation rejected", "scope", m.editor.Scope, "source", item.Key, "reason", "invalid_explanation", "policy", MemeReviewVersion)
			continue
		}
		decision := MemeDecision(review)
		m.log.Info("meme reviewed", "scope", m.editor.Scope, "source", item.Key, "batch", batch,
			"reason", review.Reason, "decision", decision, "joke_type", review.Meme.JokeType, "humor", review.Meme.Humor,
			"clarity", review.Meme.Clarity, "originality", review.Meme.Originality, "policy", MemeReviewVersion)
		if decision != "publish" && m.editor.Reviews != nil {
			audit, err := auditMeme(review)
			if err != nil {
				return err
			}
			if err = m.editor.Reviews.RejectMemeReview(ctx, m.editor.Scope, item.Key, decision, audit, m.now()); err != nil {
				return errors.New("cannot save meme review")
			}
		}
	}
	return nil
}
func acceptedMemes(result Selection, choices []daily.Item) ([]daily.Item, error) {
	reviews := make([]Review, 0, len(result.Reviews))
	for _, r := range result.Reviews {
		if MemeDecision(r) == "publish" {
			reviews = append(reviews, r)
		}
	}
	sort.Slice(reviews, func(i, j int) bool {
		a, b := memeScore(reviews[i]), memeScore(reviews[j])
		if a == b {
			return *reviews[i].Index < *reviews[j].Index
		}
		return a > b
	})
	accepted := make([]daily.Item, 0, len(reviews))
	for _, r := range reviews {
		item := choices[*r.Index]
		audit, err := auditMeme(r)
		if err != nil {
			return nil, err
		}
		item.GenerationPolicy = MemeReviewVersion
		item.SourceEvidence = audit
		accepted = append(accepted, item)
	}
	return accepted, nil
}
func memeScore(r Review) int { return 4*r.Meme.Humor + 2*r.Meme.Clarity + r.Meme.Originality }

func imageFailureErrorCode(err error) string {
	if failure, ok := errors.AsType[*imageFailureError](err); ok {
		return failure.code
	}
	return "image_processing_failed"
}

// ValidateMemeSelection verifies the complete structural contract before any candidate is used.
func ValidateMemeSelection(s Selection, count int) error {
	if s.Index == nil || *s.Index < -1 || *s.Index >= count || len(s.Reviews) != count || s.Text != "" || s.Evidence != "" {
		return &ValidationError{Reason: "meme_review_count_or_index"}
	}
	seen := make(map[int]bool, count)
	eligible := make(map[int]bool, count)
	for _, r := range s.Reviews {
		if r.Index == nil || *r.Index < 0 || *r.Index >= count || seen[*r.Index] || r.Detail != "" {
			return &ValidationError{Reason: "meme_review_fields"}
		}
		if err := validateMemeAssessment(r); err != nil {
			return err
		}
		seen[*r.Index] = true
		eligible[*r.Index] = r.Reason == reviewEligible
	}
	anyEligible := false
	for _, value := range eligible {
		anyEligible = anyEligible || value
	}
	if (*s.Index == -1 && anyEligible) || (*s.Index >= 0 && !eligible[*s.Index]) {
		return &ValidationError{Reason: "meme_review_contradiction"}
	}
	return nil
}

// Facts retain their existing schema. Vision additionally requires a complete,
// validated per-image decision, so a bare index cannot silently approve a meme.
func SelectionSchema(parts []Part) map[string]any {
	for _, p := range parts {
		if p.ResponseSchema != nil {
			return p.ResponseSchema
		}
	}
	for _, part := range parts {
		if part.FactCandidates > 0 {
			return FactSelectionSchema(part.FactCandidates)
		}
	}
	properties := map[string]any{schemaIndexKey: map[string]any{schemaTypeKey: schemaInteger}, schemaTextKey: map[string]any{schemaTypeKey: schemaString}, schemaEvidenceKey: map[string]any{schemaTypeKey: schemaString}}
	required := []string{"index", schemaTextKey, schemaEvidenceKey}
	count := 0
	for _, p := range parts {
		if p.Inline != nil {
			count++
		}
	}
	if count > 0 {
		return memeSelectionSchema(count)
	}

	return map[string]any{schemaTypeKey: schemaObject, schemaAdditionalKey: false, schemaPropertiesKey: properties, schemaRequiredKey: required}
}

func (m memeReviewSession) selectBatch(ctx context.Context, choices []daily.Item, parts []Part, batch int, approved []daily.Item) ([]daily.Item, bool, error) {
	result, err := m.editor.Generator.Generate(ctx, MemeReviewInstruction, parts)
	if err != nil {
		m.log.Warn("meme reserve refill failed", "scope", m.editor.Scope, "reason", "generator_failed")
		if len(approved) > 0 {
			return approved, true, nil
		}
		return nil, true, err
	}
	if err = ValidateMemeSelection(result, len(choices)); err != nil {
		return nil, true, err
	}
	if err = m.saveReviews(ctx, result, choices, batch); err != nil {
		return nil, true, err
	}
	selected, err := acceptedMemes(result, choices)
	if err != nil {
		return nil, true, err
	}
	provider, _, _ := strings.Cut(m.editor.Scope, ":")
	for n := range selected {
		selected[n].AIProvider = provider
		selected[n].AIModel = generatorModel(m.editor.Generator)
	}
	approved = append(approved, selected...)
	return approved, false, nil
}

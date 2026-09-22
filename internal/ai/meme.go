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
const MemeReviewVersion = "meme-v2"

type Review struct {
	Index  *int   `json:"index"`
	Reason string `json:"reason"`
	Detail string `json:"detail"`
}

type ReviewCache interface {
	MemeRejected(context.Context, string, string, time.Time) (bool, error)
	RejectMeme(context.Context, string, string, time.Time) error
}

const memeInstruction = `Ты редактор утренней рубрики для русскоязычного дружеского киноклуба. Оцени сами картинки, включая текст на них. Выбери понятный без дополнительного контекста смешной мем, лучше о кино или повседневной жизни. Исключи рекламу, политическую агитацию, порнографию, жестокость, унижение групп людей, спойлеры и посты-вопросы без шутки. Не выбирай мем только за заголовок.
Верни JSON с полями index, text, evidence, reviews. index — номер выбранного кандидата с нуля или -1, если ни один не подходит. text и evidence — пустые строки. reviews — оценка КАЖДОГО кандидата ровно один раз: index, reason, detail. reason: accepted (подходит), unreadable_text (текст картинки не удаётся прочитать), context_required (непонятно без внешнего контекста), not_meme (нет шутки), unsuitable (нарушает перечисленные ограничения), low_humor (понятно, но не смешно). detail — краткая конкретная причина по-русски, до 100 символов. У выбранного кандидата reason обязан быть accepted. Если есть accepted, выбери одного из них; -1 допустим только когда все отклонены.`

func (c *Editor) SelectMeme(ctx context.Context, items []daily.Item) (*daily.Item, error) {
	if c.MaxImages <= 0 {
		return nil, errors.New("invalid image batch size")
	}
	maxBatches := c.MaxBatches
	if maxBatches <= 0 {
		maxBatches = 2
	}
	now := c.Now
	if now == nil {
		now = time.Now
	}
	log := c.Log
	if log == nil {
		log = slog.Default()
	}
	next := 0
	for batch := 0; batch < maxBatches && next < len(items); batch++ {
		var choices []daily.Item
		parts := []Part{{Text: "Выбери один мем из пронумерованных кандидатов."}}
		for next < len(items) && len(choices) < c.MaxImages {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			i := items[next]
			next++
			if c.Reviews != nil {
				rejected, err := c.Reviews.MemeRejected(ctx, c.Scope, i.Key, now())
				if err != nil {
					return nil, errors.New("meme review cache unavailable")
				}
				if rejected {
					continue
				}
			}
			media, err := c.image(ctx, i.Image)
			if err != nil {
				log.Info("meme image skipped", "scope", c.Scope, "source", i.Key)
				continue
			}
			parts = append(parts, Part{Text: fmt.Sprintf("Кандидат %d, заголовок: %s", len(choices), i.Text)}, Part{Inline: media})
			choices = append(choices, i)
		}
		if len(choices) == 0 {
			return nil, ctx.Err()
		}
		result, err := c.Generator.Generate(ctx, memeInstruction, parts)
		if err != nil {
			return nil, err
		}
		if err := validateReviews(result, len(choices)); err != nil {
			return nil, err
		}
		for _, review := range result.Reviews {
			i := choices[*review.Index]
			log.Info("meme reviewed", "scope", c.Scope, "source", i.Key, "batch", batch+1, "reason", review.Reason, "detail", review.Detail)
			if review.Reason != "accepted" && c.Reviews != nil {
				if err := c.Reviews.RejectMeme(ctx, c.Scope, i.Key, now()); err != nil {
					return nil, errors.New("cannot save meme rejection")
				}
			}
		}
		if *result.Index >= 0 {
			return &choices[*result.Index], nil
		}
	}
	return nil, nil
}

func validateReviews(s Selection, count int) error {
	if s.Index == nil || *s.Index < -1 || *s.Index >= count || len(s.Reviews) != count {
		return errors.New("invalid meme review indexes")
	}
	seen := make(map[int]bool, count)
	accepted := make(map[int]bool, count)
	for _, r := range s.Reviews {
		if r.Index == nil || *r.Index < 0 || *r.Index >= count || seen[*r.Index] || strings.TrimSpace(r.Detail) == "" || utf8.RuneCountInString(r.Detail) > 160 {
			return errors.New("invalid meme review")
		}
		seen[*r.Index] = true
		switch r.Reason {
		case "accepted":
			accepted[*r.Index] = true
		case "unreadable_text", "context_required", "not_meme", "unsuitable", "low_humor":
		default:
			return errors.New("unknown meme review reason")
		}
	}
	if (*s.Index == -1 && len(accepted) != 0) || (*s.Index >= 0 && !accepted[*s.Index]) {
		return errors.New("meme selection contradicts reviews")
	}
	return nil
}

// Facts retain their existing schema. Vision additionally requires a complete,
// validated per-image decision, so a bare index cannot silently approve a meme.
func SelectionSchema(parts []Part) map[string]any {
	properties := map[string]any{"index": map[string]any{"type": "integer"}, "text": map[string]any{"type": "string"}, "evidence": map[string]any{"type": "string"}}
	required := []string{"index", "text", "evidence"}
	for _, p := range parts {
		if p.Inline == nil {
			continue
		}
		properties["reviews"] = map[string]any{"type": "array", "items": map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{
				"index":  map[string]any{"type": "integer"},
				"reason": map[string]any{"type": "string", "enum": []string{"accepted", "unreadable_text", "context_required", "not_meme", "unsuitable", "low_humor"}},
				"detail": map[string]any{"type": "string"},
			}, "required": []string{"index", "reason", "detail"},
		}}
		required = append(required, "reviews")
		break
	}
	return map[string]any{"type": "object", "additionalProperties": false, "properties": properties, "required": required}
}

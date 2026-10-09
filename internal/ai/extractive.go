package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/vaporon4a/movie-helper/internal/daily"
	"github.com/vaporon4a/movie-helper/internal/facts"
)

const factSelectionInstruction = `Ты редактор рубрики интересных историй о создании кино для русскоязычного дружеского чата. Тексты уже на русском. Не переписывай, не переводь и не дополняй их. Оцени каждый эпизод: понятен ли он обычному читателю без статьи и действительно ли хочется пересказать его друзьям?
Фильм указан отдельно; слово «фильм» не обязано повторяться в эпизоде. Имена должны иметь понятные роли, причинные связи — явно объясняться. Отклоняй обрывки с «он», «это», «однако» без контекста, бюрократическую хронику смены сценаристов, перечисления имён и дат, неясный перевод, слухи, сюжет и спойлеры. Хорошие темы: неожиданный практический трюк, забавный случай со съёмок, удивительное решение, понятная история с развязкой. Короткий самостоятельный факт может быть лучше длинного.
Верни index лучшего принятого кандидата или -1, text и evidence строго пустые строки. reviews: одна оценка КАЖДОГО номера строкой, reason из схемы, clarity (понятность) и interest (интерес) от 1 до 5. accepted только при обеих оценках >=4. При отсутствии accepted верни -1. Не пытайся спасать слабые фрагменты придуманными деталями.`

type EpisodeSelector struct {
	Generator Generator
	Provider  string
	Log       *slog.Logger
}

func (s EpisodeSelector) SelectEpisodes(ctx context.Context, episodes []facts.Episode) ([]daily.Item, error) {
	if len(episodes) == 0 {
		return nil, nil
	}
	data, err := json.Marshal(episodes)
	if err != nil {
		return nil, err
	}
	result, err := s.Generator.Generate(ctx, factSelectionInstruction+SourceInstruction, []Part{{Text: string(data), FactCandidates: len(episodes)}})
	if err != nil {
		return nil, err
	}
	if err = validateEpisodeSelection(result, len(episodes)); err != nil {
		return nil, err
	}
	if s.Log != nil {
		for _, review := range result.Reviews {
			s.Log.Info("fact episode reviewed", "provider", s.Provider, "movie_id", episodes[*review.Index].Movie.ID, "key", episodes[*review.Index].Key, "reason", review.Reason, "interest", review.Interest, "clarity", review.Clarity, "policy", daily.FactPolicy)
		}
	}
	if *result.Index == -1 {
		return nil, nil
	}
	return materializeEpisodes(result, episodes, s.Provider)
}
func materializeEpisodes(result Selection, episodes []facts.Episode, provider string) ([]daily.Item, error) {
	accepted := map[int]bool{}
	for _, r := range result.Reviews {
		if r.Reason == reviewAccepted {
			accepted[*r.Index] = true
		}
	}
	order := []int{*result.Index}
	for n := range episodes {
		if n != *result.Index && accepted[n] {
			order = append(order, n)
		}
	}
	out := make([]daily.Item, 0, 3)
	movies := map[int64]bool{}
	for _, n := range order {
		e := episodes[n]
		if movies[e.Movie.ID] {
			continue
		}
		movies[e.Movie.ID] = true
		audit, err := json.Marshal(e)
		if err != nil {
			return nil, err
		}
		out = append(out, daily.Item{Kind: daily.Fact, Text: fmt.Sprintf("«%s» (%d)\n\n%s\n\n%s", e.Movie.Title, e.Movie.Year, e.Text, daily.WikipediaExtractiveAttribution),
			Source: e.URL, Key: e.Key, SourceEvidence: string(audit), AIProvider: provider, GenerationPolicy: daily.FactPolicy})
		if len(out) == 3 {
			break
		}
	}
	return out, nil
}
func validateEpisodeSelection(s Selection, count int) error {
	if s.Index == nil || *s.Index < -1 || *s.Index >= count || s.Text != "" || s.Evidence != "" || len(s.Reviews) != count {
		return &ValidationError{Reason: "fact_ids_contract"}
	}
	seen := map[int]bool{}
	accepted := map[int]bool{}
	for _, r := range s.Reviews {
		if r.Index == nil || *r.Index < 0 || *r.Index >= count || seen[*r.Index] || r.Interest < 1 || r.Interest > 5 || r.Clarity < 1 || r.Clarity > 5 || strings.TrimSpace(r.Detail) != "" {
			return &ValidationError{Reason: "fact_review_contract"}
		}
		seen[*r.Index] = true
		acceptedReview, err := acceptedEpisodeReview(r)
		if err != nil {
			return err
		}
		if acceptedReview {
			accepted[*r.Index] = true
		}
	}
	if (*s.Index == -1 && len(accepted) > 0) || (*s.Index >= 0 && !accepted[*s.Index]) {
		return &ValidationError{Reason: "fact_review_contradiction"}
	}
	return nil
}
func FactSelectionSchema(count int) map[string]any {
	properties := map[string]any{}
	keys := make([]string, 0, count)
	indices := []int{-1}
	for n := 0; n < count; n++ {
		key := fmt.Sprint(n)
		keys = append(keys, key)
		indices = append(indices, n)
		properties[key] = map[string]any{schemaTypeKey: schemaObject, schemaAdditionalKey: false, schemaPropertiesKey: map[string]any{
			"reason":   map[string]any{schemaTypeKey: schemaString, schemaEnumKey: []string{reviewAccepted, "context_required", "uninteresting", "too_dense", "not_production", "incomplete", "unsuitable"}},
			"interest": map[string]any{schemaTypeKey: schemaInteger, schemaEnumKey: []int{1, 2, 3, 4, 5}}, "clarity": map[string]any{schemaTypeKey: schemaInteger, schemaEnumKey: []int{1, 2, 3, 4, 5}},
		}, schemaRequiredKey: []string{"reason", "interest", "clarity"}}
	}
	return map[string]any{schemaTypeKey: schemaObject, schemaAdditionalKey: false, schemaPropertiesKey: map[string]any{
		"index": map[string]any{schemaTypeKey: schemaInteger, schemaEnumKey: indices}, schemaTextKey: map[string]any{schemaTypeKey: schemaString, schemaEnumKey: []string{""}}, schemaEvidenceKey: map[string]any{schemaTypeKey: schemaString, schemaEnumKey: []string{""}},
		"reviews": map[string]any{schemaTypeKey: schemaObject, schemaAdditionalKey: false, schemaPropertiesKey: properties, schemaRequiredKey: keys},
	}, schemaRequiredKey: []string{"index", schemaTextKey, schemaEvidenceKey, "reviews"}}
}

func acceptedEpisodeReview(r Review) (bool, error) {
	switch r.Reason {
	case reviewAccepted:
		if r.Interest < 4 || r.Clarity < 4 {
			return false, &ValidationError{Reason: "fact_low_score"}
		}
		return true, nil
	case "context_required", "uninteresting", "too_dense", "not_production", "incomplete", "unsuitable":
		return false, nil
	default:
		return false, &ValidationError{Reason: "fact_review_reason"}
	}
}

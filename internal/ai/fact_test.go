package ai

import (
	"context"
	"errors"
	"strings"
	"testing"
)

const validFactText = "Для съёмок сцены команда построила вращающийся коридор и закрепила его на больших кольцах. Два мощных электромотора приводили всю конструкцию в движение."

func TestFactAcceptsLongerExactQuoteWithoutRetry(t *testing.T) {
	quote := "The corridor was suspended along eight large concentric rings that were spaced equidistantly outside its walls and powered by two massive electric motors."
	index, calls := 0, 0
	ed := &Editor{Generator: generatorFunc(func(context.Context, string, []Part) (Selection, error) {
		calls++
		return Selection{Index: &index, Text: validFactText, Evidence: quote}, nil
	})}
	item, err := ed.Fact(context.Background(), []Article{{Text: quote}})
	if err != nil || item == nil || calls != 2 || item.GenerationPolicy != FactGenerationPolicy {
		t.Fatal(item, err, calls)
	}
}

func TestFactRepairsLongQuoteOnceAndPreservesValidation(t *testing.T) {
	quote := "The corridor was suspended along eight large concentric rings that were spaced equidistantly outside its walls and powered by two massive electric motors."
	quote += " " + quote + " " + quote // 69 words: exceeds the evidence ceiling.
	short := "powered by two massive electric motors"
	article := Article{Text: quote, URL: "https://en.wikipedia.org/w/index.php?oldid=123", Key: "wikipedia:Inception", Attribution: "Wikipedia"}
	index := 0
	for _, tc := range []struct {
		name     string
		evidence string
		err      error
		wantItem bool
	}{
		{"corrected", short, nil, true},
		{"still_long", quote, nil, false},
		{"invented", "powered by three small electric motors", nil, false},
		{"budget_exhausted", "", ErrDailyLimit, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			ed := &Editor{Scope: "gemini:model:" + FactGenerationPolicy, Generator: generatorFunc(func(_ context.Context, instruction string, parts []Part) (Selection, error) {
				calls++
				if calls == 1 {
					return Selection{Index: &index, Text: validFactText, Evidence: quote}, nil
				}
				if calls == 2 && (len(parts) != 2 || !strings.Contains(instruction, "Предыдущий ответ не прошёл") || !strings.Contains(parts[0].Text, quote)) {
					t.Fatal("correction lost source or exceeded retry bound")
				}
				if calls > 3 || (calls == 3 && !strings.Contains(instruction, "строгий редактор")) {
					t.Fatal("unexpected fact generation pass")
				}
				return Selection{Index: &index, Text: validFactText, Evidence: tc.evidence}, tc.err
			})}
			item, err := ed.Fact(context.Background(), []Article{article})
			wantCalls := 2
			if tc.wantItem {
				wantCalls = 3
			}
			if calls != wantCalls || (item != nil) != tc.wantItem || (err == nil) != tc.wantItem {
				t.Fatal(calls, item, err)
			}
			if tc.err != nil && !errors.Is(err, tc.err) {
				t.Fatal("provider error not preserved", err)
			}
			if item != nil && (item.Source != article.URL || item.Key != article.Key || item.SourceEvidence != short || item.AIProvider != "gemini") {
				t.Fatal("source provenance changed", item)
			}
		})
	}
}

func TestFactRepairsNonliteralQuote(t *testing.T) {
	index, calls := 0, 0
	source := "The team built a corridor that rotated a full 360 degrees using electric motors."
	ed := &Editor{Generator: generatorFunc(func(_ context.Context, instruction string, _ []Part) (Selection, error) {
		calls++
		quote := "corridor... rotated a full 360 degrees"
		if calls >= 2 {
			if !strings.Contains(instruction, "fact_source_evidence") {
				if calls == 2 {
					t.Fatal("missing correction reason")
				}
			}
			quote = "a corridor that rotated a full 360 degrees"
		}
		return Selection{Index: &index, Text: validFactText, Evidence: quote}, nil
	})}
	item, err := ed.Fact(context.Background(), []Article{{Text: source}})
	if err != nil || item == nil || calls != 3 {
		t.Fatal(item, err, calls)
	}
}

func TestFactQualityGateRejectsUnreadableRussian(t *testing.T) {
	for _, tc := range []struct {
		text, reason string
	}{
		{"Слишком коротко. Совсем сухо.", "fact_word_count"},
		{"Первое предложение содержит достаточно слов для проверки качества итогового текста и его длины. Второе предложение содержит слово службыكافحة и поэтому должно быть отклонено локально.", "fact_unexpected_script"},
		{"Первое предложение содержит достаточно слов для проверки качества итогового текста и его длины. Второе предложение содержит слoво со смешанными алфавитами и должно быть отклонено.", "fact_mixed_script"},
		{"Это один длинный текст без корректного завершения и без второго предложения хотя слов здесь вполне достаточно для прохождения проверки минимальной длины текста", "fact_sentence_count"},
	} {
		if got := validateFactText(tc.text); got != tc.reason {
			t.Fatalf("text=%q reason=%s want=%s", tc.text, got, tc.reason)
		}
	}
}

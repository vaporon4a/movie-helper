package ai

import (
	"context"
	"errors"
	"strings"
	"testing"
)

const validFactText = "Для съёмок сцены команда построила вращающийся коридор и закрепила его на восьми больших кольцах. Два мощных электромотора приводили всю конструкцию в движение, пока камера оставалась неподвижной относительно декорации. Благодаря этой установке актёры могли двигаться по стенам и потолку прямо во время съёмки, а сложный эффект получался практически без компьютерной графики."

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
	quote += " " + quote + " " + quote + " " + quote // 92 words: exceeds the evidence ceiling.
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
		{"Первое предложение содержит достаточно слов для проверки качества итогового текста и его длины. Второе предложение подробно продолжает описание материала и содержит слово службыكافحة, которое должно быть отклонено локально. Третье предложение добавляет нейтральный контекст, чтобы общий объём примера соответствовал новому минимальному пределу проверки опубликованного факта.", "fact_unexpected_script"},
		{"Первое предложение содержит достаточно слов для проверки качества итогового текста и его длины. Второе предложение подробно продолжает описание материала и содержит слoво со смешанными алфавитами, которое должно быть отклонено. Третье предложение добавляет нейтральный контекст, чтобы общий объём примера соответствовал новому минимальному пределу проверки опубликованного факта.", "fact_mixed_script"},
		{"Это один длинный текст без корректного завершения и без второго предложения хотя слов здесь вполне достаточно для прохождения проверки минимальной длины итогового материала поэтому проверка должна увидеть отсутствие нужной структуры несмотря на общий объём содержание и используемый русский алфавит в этом искусственном тестовом примере качества", "fact_sentence_count"},
	} {
		if got := validateFactText(tc.text); got != tc.reason {
			t.Fatalf("text=%q reason=%s want=%s", tc.text, got, tc.reason)
		}
	}
}

func TestFactQualityGateBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name             string
		words, sentences int
		want             string
	}{
		{"below_minimum", 29, 2, "fact_word_count"},
		{"minimum", 30, 2, ""},
		{"maximum", 110, 5, ""},
		{"above_maximum", 111, 5, "fact_word_count"},
		{"too_few_sentences", 30, 1, "fact_sentence_count"},
		{"too_many_sentences", 30, 6, "fact_sentence_count"},
		{"sentences_too_long", 99, 3, "fact_sentence_too_long"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := validateFactText(factTextFixture(tc.words, tc.sentences)); got != tc.want {
				t.Fatalf("reason=%q want=%q", got, tc.want)
			}
		})
	}
}

func factTextFixture(words, sentences int) string {
	parts := make([]string, sentences)
	remaining := words
	for i := range parts {
		count := remaining / (sentences - i)
		parts[i] = strings.TrimSpace(strings.Repeat("слово ", count)) + "."
		remaining -= count
	}
	return strings.Join(parts, " ")
}

func TestFactQualityGateRejectsCompressedFact(t *testing.T) {
	text := "Стивен Спилберг признал персонажей сценария Питера Бенчли в картине «Челюсти» непопулярными и предложил молодому сценаристу Джону Байраму переработать текст. Однако Байрам отказался от предложения режиссёра."
	if got := validateFactText(text); got != "fact_word_count" {
		t.Fatalf("compressed fact reason=%q want=fact_word_count", got)
	}
}

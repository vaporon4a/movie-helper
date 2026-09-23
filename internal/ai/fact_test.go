package ai

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestFactAcceptsLongerExactQuoteWithoutRetry(t *testing.T) {
	quote := "The corridor was suspended along eight large concentric rings that were spaced equidistantly outside its walls and powered by two massive electric motors."
	index, calls := 0, 0
	ed := &Editor{Generator: generatorFunc(func(context.Context, string, []Part) (Selection, error) {
		calls++
		return Selection{Index: &index, Text: "Коридор вращали два электромотора.", Evidence: quote}, nil
	})}
	item, err := ed.Fact(context.Background(), []Article{{Text: quote}})
	if err != nil || item == nil || calls != 1 {
		t.Fatal(item, err, calls)
	}
}

func TestFactRepairsLongQuoteOnceAndPreservesValidation(t *testing.T) {
	quote := "The corridor was suspended along eight large concentric rings that were spaced equidistantly outside its walls and powered by two massive electric motors."
	quote += " " + quote // 46 words: exceeds the hard ceiling.
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
			ed := &Editor{Generator: generatorFunc(func(_ context.Context, instruction string, parts []Part) (Selection, error) {
				calls++
				if calls == 1 {
					return Selection{Index: &index, Text: "Коридор вращали два электромотора.", Evidence: quote}, nil
				}
				if calls > 2 || len(parts) != 2 || !strings.Contains(instruction, "Предыдущий ответ не прошёл") || !strings.Contains(parts[0].Text, quote) {
					t.Fatal("correction lost source or exceeded retry bound")
				}
				return Selection{Index: &index, Text: "Коридор вращали два электромотора.", Evidence: tc.evidence}, tc.err
			})}
			item, err := ed.Fact(context.Background(), []Article{article})
			if calls != 2 || (item != nil) != tc.wantItem || (err == nil) != tc.wantItem {
				t.Fatal(calls, item, err)
			}
			if tc.err != nil && !errors.Is(err, tc.err) {
				t.Fatal("provider error not preserved", err)
			}
			if item != nil && (item.Source != article.URL || item.Key != article.Key) {
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
		if calls == 2 {
			if !strings.Contains(instruction, "fact_source_evidence") {
				t.Fatal("missing correction reason")
			}
			quote = "a corridor that rotated a full 360 degrees"
		}
		return Selection{Index: &index, Text: "Коридор вращался на 360 градусов.", Evidence: quote}, nil
	})}
	item, err := ed.Fact(context.Background(), []Article{{Text: source}})
	if err != nil || item == nil || calls != 2 {
		t.Fatal(item, err, calls)
	}
}

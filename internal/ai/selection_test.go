package ai

import (
	"encoding/json"
	"testing"
)

func TestReviewSchemaHasExactlyOneFieldPerImage(t *testing.T) {
	for _, count := range []int{1, 4} {
		parts := make([]Part, 1, 1+count)
		parts[0] = Part{Text: "candidates"}
		for range count {
			parts = append(parts, Part{Inline: &Inline{}})
		}
		s := SelectionSchema(parts)
		props := schemaValue[map[string]any](t, s["properties"])
		reviews := schemaValue[map[string]any](t, props["reviews"])
		if reviews["type"] != "object" || reviews["additionalProperties"] != false || len(schemaValue[[]string](t, reviews["required"])) != count || len(schemaValue[map[string]any](t, reviews["properties"])) != count {
			t.Fatal(reviews)
		}
	}
}

func TestSelectionDecodesKeyedReviewsAndRejectsRepeatedArray(t *testing.T) {
	var s Selection
	if err := json.Unmarshal([]byte(`{"index":-1,"text":"","evidence":"","reviews":{"0":{"reason":"context_required","assessment":{"joke_type":"visual","observation":"Кадр игры без подписи","punchline":"","humor":1,"clarity":2,"originality":1}}}}`), &s); err != nil {
		t.Fatal(err)
	}
	if err := ValidateMemeSelection(s, 1); err != nil {
		t.Fatal(err)
	}
	// Regression: Qwen returned five separate reviews of the same input image.
	old := `{"index":-1,"reviews":[{"index":0,"reason":"context_required","detail":"a"},{"index":0,"reason":"context_required","detail":"b"}]}`
	if err := json.Unmarshal([]byte(old), &s); err == nil {
		t.Fatal("unbounded review array accepted")
	}
	for _, key := range []string{"-1", "01", "text"} {
		data := `{"index":-1,"reviews":{"` + key + `":{"reason":"not_meme","detail":"reason"}}}`
		if err := json.Unmarshal([]byte(data), &s); err == nil {
			t.Fatal("invalid candidate key", key)
		}
	}
	if err := json.Unmarshal([]byte(`{"index":0,"text":"fact","evidence":"exact quote"}`), &s); err != nil || len(s.Reviews) != 0 {
		t.Fatal("fact decoding changed", err)
	}
}

func schemaValue[T any](t *testing.T, value any) T {
	t.Helper()
	result, ok := value.(T)
	if !ok {
		t.Fatalf("unexpected schema value: %T", value)
	}
	return result
}

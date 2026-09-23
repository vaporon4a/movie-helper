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
		props := s["properties"].(map[string]any)
		reviews := props["reviews"].(map[string]any)
		if reviews["type"] != "object" || reviews["additionalProperties"] != false || len(reviews["required"].([]string)) != count || len(reviews["properties"].(map[string]any)) != count {
			t.Fatal(reviews)
		}
	}
}

func TestSelectionDecodesKeyedReviewsAndRejectsRepeatedArray(t *testing.T) {
	var s Selection
	if err := json.Unmarshal([]byte(`{"index":-1,"text":"","evidence":"","reviews":{"0":{"reason":"context_required","detail":"Нужен контекст"}}}`), &s); err != nil {
		t.Fatal(err)
	}
	if err := validateReviews(s, 1); err != nil {
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

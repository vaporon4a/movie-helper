package ai

import (
	"encoding/json"
	"errors"
	"strconv"
)

type ValidationError struct{ Reason string }

func (e *ValidationError) Error() string { return "AI validation: " + e.Reason }

// Model responses use a closed object keyed by candidate number. An unbounded
// reviews array allowed a strict-schema model to repeat one review many times.
func (s *Selection) UnmarshalJSON(data []byte) error {
	var wire struct {
		Index    *int   `json:"index"`
		Text     string `json:"text"`
		Evidence string `json:"evidence"`
		Reviews  map[string]struct {
			Reason string `json:"reason"`
			Detail string `json:"detail"`
		} `json:"reviews"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return errors.New("invalid selection object")
	}
	*s = Selection{Index: wire.Index, Text: wire.Text, Evidence: wire.Evidence}
	for key, r := range wire.Reviews {
		n, err := strconv.Atoi(key)
		if err != nil || n < 0 || strconv.Itoa(n) != key {
			return errors.New("invalid review key")
		}
		s.Reviews = append(s.Reviews, Review{Index: &n, Reason: r.Reason, Detail: r.Detail})
	}
	return nil
}

package feedback

import (
	"errors"
	"testing"
	"time"
)

func TestRejectsMalformedReactionEvents(t *testing.T) {
	valid := Event{UpdateID: 1, ChatID: -1, MessageID: 2, Mode: Delta,
		OccurredAt: 10, ReceivedAt: time.Unix(12, 0), After: []Reaction{{Type: Emoji, Value: "👍", Count: 1}}}
	for _, change := range []func(*Event){
		func(e *Event) { e.Mode = "unknown" },
		func(e *Event) { e.ChatID = 1 },
		func(e *Event) { e.MessageID = 0 },
		func(e *Event) { e.After = append(e.After, e.After[0]) },
		func(e *Event) { e.After = []Reaction{{Type: Emoji, Value: "", Count: 1}} },
		func(e *Event) { e.After = []Reaction{{Type: Emoji, Value: "👍", Count: -1}} },
		func(e *Event) { e.After = []Reaction{{Type: Custom, Value: "not-an-id", Count: 1}} },
		func(e *Event) { e.Before = e.After; e.Mode = Snapshot },
	} {
		event := valid
		change(&event)
		if !errors.Is(event.Validate(), ErrInvalid) {
			t.Fatal(event)
		}
	}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
}

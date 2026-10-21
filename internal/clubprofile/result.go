package clubprofile

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

func (q Question) Validate() error {
	if !q.Topic.Valid() || q.Intent == "" || q.Version == "" || q.Scale == "" || !q.Anonymous || q.Multiple {
		return ErrInvalid
	}
	if strings.TrimSpace(q.Text) == "" || utf8.RuneCountInString(q.Text) > 300 || len(q.Options) < 2 || len(q.Options) > 12 {
		return ErrInvalid
	}
	keys := make(map[string]bool, len(q.Options))
	for _, option := range q.Options {
		if option.Key == "" || keys[option.Key] || strings.TrimSpace(option.Text) == "" || utf8.RuneCountInString(option.Text) > 100 {
			return ErrInvalid
		}
		keys[option.Key] = true
	}
	return nil
}

// ValidateFinal rejects absent and inconsistent snapshots rather than inventing zero votes.
func (r PollResult) ValidateFinal(q Question, pollID string) error {
	if !r.Closed || r.PollID == "" || r.PollID != pollID || r.Total == nil || *r.Total < 0 || len(r.Counts) != len(q.Options) || r.ReceivedAt.IsZero() {
		return ErrInvalid
	}
	if r.Source != SourceUpdate && r.Source != SourceStop {
		return ErrInvalid
	}
	total := 0
	for _, count := range r.Counts {
		if count < 0 || count > *r.Total || total > *r.Total-count {
			return fmt.Errorf("poll totals inconsistent: %w", ErrInvalid)
		}
		total += count
	}
	if q.Multiple || total != *r.Total {
		return ErrInvalid
	}
	return nil
}

func (o Observation) Answers() int {
	if o.Result.ValidateFinal(o.Question, o.Result.PollID) != nil {
		return 0
	}
	n := 0
	for i, option := range o.Question.Options {
		if option.Substantive {
			n += o.Result.Counts[i]
		}
	}
	return n
}

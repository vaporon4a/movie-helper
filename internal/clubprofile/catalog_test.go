package clubprofile

import (
	"context"
	"reflect"
	"testing"
	"time"
)

type catalogHistory struct {
	polls []Poll
	chat  int64
}

func (h *catalogHistory) ProfilePolls(_ context.Context, chat int64, _ PollState) ([]Poll, error) {
	h.chat = chat
	return h.polls, nil
}

func TestCatalogQuestionsAreStableAnonymousAndRotateHistory(t *testing.T) {
	now := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	history := &catalogHistory{}
	source := ProfileQuestions{History: history}
	for _, topic := range []Topic{Genre, Reference} {
		first, err := source.InterestQuestion(t.Context(), topic, -1, 42, now)
		if err != nil {
			t.Fatal(err)
		}
		repeated, err := source.InterestQuestion(t.Context(), topic, -1, 42, now)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(first, repeated) || !first.Anonymous || !first.Revoting || first.Intent != Interest {
			t.Fatal("question contract changed")
		}
		want := 10
		if topic == Reference {
			want = 9
		}
		if len(first.Options) != want {
			t.Fatal(topic, len(first.Options))
		}
		used := map[string]bool{}
		for _, o := range first.Options {
			if used[o.Key] {
				t.Fatal("duplicate option")
			}
			used[o.Key] = true
		}
		if topic == Reference {
			if first.Options[8].Substantive || first.Options[8].Key != "unfamiliar" {
				t.Fatal("missing neutral option")
			}
			history.polls = []Poll{{Question: first, MessageID: 1, OpenedAt: now.Add(-time.Hour)}}
			rotated, e := source.InterestQuestion(t.Context(), topic, -1, 42, now)
			if e != nil {
				t.Fatal(e)
			}
			for _, o := range rotated.Options[:8] {
				if used[o.Key] {
					t.Fatal("recent film repeated", o)
				}
			}
			history.polls[0].OpenedAt = now.Add(-9 * 7 * 24 * time.Hour)
			expired, e := source.InterestQuestion(t.Context(), topic, -1, 42, now)
			if e != nil {
				t.Fatal(e)
			}
			if !reflect.DeepEqual(first, expired) || history.chat != -1 {
				t.Fatal("history window or chat scope changed")
			}
		}
	}
}

func TestWeeklySlotDSTGapAndFold(t *testing.T) {
	for _, tc := range []struct {
		now  time.Time
		want string
	}{
		{time.Date(2026, 3, 29, 0, 0, 0, 0, time.UTC), "2026-03-29T01:00:00Z"},
		{time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC), "2026-10-25T00:30:00Z"},
	} {
		got, err := WeeklySlot(tc.now, int(time.Sunday), "02:30", "Europe/Berlin")
		if err != nil || got.UTC().Format(time.RFC3339) != tc.want {
			t.Fatal(got, err, tc.want)
		}
	}
}

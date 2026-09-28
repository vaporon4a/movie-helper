package featurevote

import (
	"testing"
	"time"
)

func TestWeeklySlotUsesChatTimezone(t *testing.T) {
	tests := []struct {
		name, now, zone, clock, want string
		weekday                      int
	}{
		{name: "same day", now: "2026-09-28T05:00:00Z", zone: "Europe/Moscow", weekday: 1, clock: "18:00", want: "2026-09-28T15:00:00Z"},
		{name: "previous day for catchup", now: "2026-09-28T01:00:00Z", zone: "Europe/Moscow", weekday: 0, clock: "23:30", want: "2026-09-27T20:30:00Z"},
		{name: "spring gap moves forward", now: "2026-03-29T00:00:00Z", zone: "Europe/Berlin", weekday: 0, clock: "02:30", want: "2026-03-29T01:00:00Z"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			now, err := time.Parse(time.RFC3339, tc.now)
			if err != nil {
				t.Fatal(err)
			}
			want, err := time.Parse(time.RFC3339, tc.want)
			if err != nil {
				t.Fatal(err)
			}
			got, err := WeeklySlot(now, tc.weekday, tc.clock, tc.zone)
			if err != nil || !got.Equal(want) {
				t.Fatalf("slot=%s want=%s err=%v", got, want, err)
			}
		})
	}
}

func TestWeeklySlotRejectsInvalidSettings(t *testing.T) {
	if _, err := WeeklySlot(time.Now(), 7, "18:00", "Europe/Moscow"); err == nil {
		t.Fatal("invalid weekday accepted")
	}
	if _, err := WeeklySlot(time.Now(), 1, "bad", "Europe/Moscow"); err == nil {
		t.Fatal("invalid clock accepted")
	}
}

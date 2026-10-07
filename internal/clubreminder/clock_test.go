package clubreminder

import (
	"testing"
	"time"
)

func TestMonthlySlotsAndLast(t *testing.T) {
	settings := Settings{Weekday: 5, Ordinal: 2, Clock: "19:00", Zone: "Europe/Moscow"}
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	slots, err := Slots(settings, now)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, slot := range slots {
		if slot.Cycle == "2026-10-09" {
			found[slot.At.Format("2006-01-02 15:04")] = true
		}
	}
	if !found["2026-10-19 19:00"] || !found["2026-10-26 19:00"] {
		t.Fatal(found)
	}
	for _, tc := range []struct {
		year  int
		month time.Month
		day   int
	}{{2026, 2, 27}, {2024, 2, 23}, {2026, 10, 30}} {
		got := Meeting(tc.year, tc.month, 5, 5, time.UTC)
		if got.Day() != tc.day {
			t.Fatal(got)
		}
	}
}
func TestDSTAndYearBoundary(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	missing := time.Date(2026, 3, 29, 12, 0, 0, 0, loc)
	if !localClock(missing, 2, 30).IsZero() {
		t.Fatal("nonexistent clock accepted")
	}
	repeated := localClock(time.Date(2026, 10, 25, 12, 0, 0, 0, loc), 2, 30)
	_, offset := repeated.Zone()
	if offset != 7200 {
		t.Fatal("not earliest occurrence", repeated)
	}
	slots, err := Slots(Settings{Weekday: 5, Ordinal: 5, Clock: "19:00", Zone: "Europe/Moscow"}, time.Date(2027, 1, 1, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, slot := range slots {
		if slot.Cycle == "2026-12-25" && slot.At.Format("2006-01-02") == "2027-01-04" {
			found = true
		}
	}
	if !found {
		t.Fatal("December reminder lost")
	}
}
func TestTemplatesAndSimilarity(t *testing.T) {
	for _, intent := range []string{Explore, Discuss} {
		for _, tone := range Tones {
			if !ValidText(Fallback(intent, tone)) {
				t.Fatal(intent, tone)
			}
		}
	}
	text := Fallback(Explore, "friendly").Body
	if !Similar(text, text+" !!!") {
		t.Fatal("duplicate undetected")
	}
	if Similar(text, Fallback(Discuss, "roast").Body) {
		t.Fatal("different intents classified duplicate")
	}
}

package clubreminder

import (
	"errors"
	"sort"
	"time"
)

func Validate(weekday, ordinal int, clock string) error {
	if weekday < 0 || weekday > 6 || ordinal < 1 || ordinal > 5 {
		return errors.New("invalid monthly rule")
	}
	if _, err := time.Parse("15:04", clock); err != nil {
		return errors.New("invalid clock")
	}
	return nil
}

// Ordinal five denotes the last occurrence, including months with only four.
func Meeting(year int, month time.Month, weekday, ordinal int, loc *time.Location) time.Time {
	first := time.Date(year, month, 1, 12, 0, 0, 0, loc)
	if ordinal == 5 {
		last := first.AddDate(0, 1, -1)
		return last.AddDate(0, 0, -(int(last.Weekday())-weekday+7)%7)
	}
	return first.AddDate(0, 0, (weekday-int(first.Weekday())+7)%7+7*(ordinal-1))
}
func Slots(settings Settings, now time.Time) ([]Slot, error) {
	if err := Validate(settings.Weekday, settings.Ordinal, settings.Clock); err != nil {
		return nil, err
	}
	loc, err := time.LoadLocation(settings.Zone)
	if err != nil || settings.Zone == "" {
		return nil, errors.New("set timezone first")
	}
	clock, _ := time.Parse("15:04", settings.Clock)
	local := now.In(loc)
	first := time.Date(local.Year(), local.Month(), 1, 12, 0, 0, 0, loc)
	slots := make([]Slot, 0, 8)
	for month := -1; month <= 2; month++ {
		base := first.AddDate(0, month, 0)
		meeting := Meeting(base.Year(), base.Month(), settings.Weekday, settings.Ordinal, loc)
		for index, offset := range []int{10, 17} {
			date := meeting.AddDate(0, 0, offset)
			at := localClock(date, clock.Hour(), clock.Minute())
			if at.IsZero() {
				continue
			}
			intent := Explore
			if index == 1 {
				intent = Discuss
			}
			slots = append(slots, Slot{At: at, Cycle: meeting.Format("2006-01-02"), Intent: intent})
		}
	}
	sort.Slice(slots, func(i, j int) bool { return slots[i].At.Before(slots[j].At) })
	return slots, nil
}

// Search real minutes to choose the first repeated clock and skip nonexistent clocks.
func localClock(date time.Time, hour, minute int) time.Time {
	start := date.Add(-18 * time.Hour)
	for at := start; !at.After(date.Add(18 * time.Hour)); at = at.Add(time.Minute) {
		value := at.In(date.Location())
		if value.Year() == date.Year() && value.Month() == date.Month() && value.Day() == date.Day() && value.Hour() == hour && value.Minute() == minute {
			return at
		}
	}
	return time.Time{}
}

func NextMeeting(settings Settings, now time.Time) (time.Time, error) {
	loc, err := time.LoadLocation(settings.Zone)
	if err != nil || Validate(settings.Weekday, settings.Ordinal, settings.Clock) != nil {
		return time.Time{}, errors.New("meeting rule unavailable")
	}
	local := now.In(loc)
	at := Meeting(local.Year(), local.Month(), settings.Weekday, settings.Ordinal, loc)
	if at.Format(time.DateOnly) < local.Format(time.DateOnly) {
		base := time.Date(local.Year(), local.Month()+1, 1, 12, 0, 0, 0, loc)
		at = Meeting(base.Year(), base.Month(), settings.Weekday, settings.Ordinal, loc)
	}
	return at, nil
}

package movieclub

import (
	"errors"
	"strings"
	"time"
)

func WeekdayName(value int) string {
	names := []string{"вс", "пн", "вт", "ср", "чт", "пт", "сб"}
	if value < 0 || value >= len(names) {
		return "?"
	}
	return names[value]
}

func ParseWeekday(value string) (int, bool) {
	days := map[string]int{"sun": 0, "вс": 0, "mon": 1, "пн": 1, "tue": 2, "вт": 2, "wed": 3, "ср": 3, "thu": 4, "чт": 4, "fri": 5, "пт": 5, "sat": 6, "сб": 6}
	day, ok := days[strings.ToLower(strings.TrimSpace(value))]
	return day, ok
}

func WeeklySlot(now time.Time, weekday int, clock, zone string) (time.Time, error) {
	loc, err := time.LoadLocation(zone)
	if err != nil || weekday < 0 || weekday > 6 {
		return time.Time{}, errors.New("invalid weekly schedule")
	}
	parsed, err := time.Parse("15:04", clock)
	if err != nil {
		return time.Time{}, err
	}
	local := now.In(loc)
	diff := weekday - int(local.Weekday())
	target := local.AddDate(0, 0, diff)
	year, month, day := target.Date()
	anchor := time.Date(year, month, day, 12, 0, 0, 0, loc)
	want := parsed.Hour()*60 + parsed.Minute()
	for candidate := anchor.Add(-18 * time.Hour); !candidate.After(anchor.Add(18 * time.Hour)); candidate = candidate.Add(time.Minute) {
		value := candidate.In(loc)
		candidateYear, candidateMonth, candidateDay := value.Date()
		if candidateYear == year && candidateMonth == month && candidateDay == day && value.Hour()*60+value.Minute() >= want {
			return candidate, nil
		}
	}
	return time.Time{}, errors.New("no slot on local day")
}

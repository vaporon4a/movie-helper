package telegram

import "strings"

func weekdayName(value int) string {
	names := []string{"вс", "пн", "вт", "ср", "чт", "пт", "сб"}
	if value < 0 || value >= len(names) {
		return "?"
	}
	return names[value]
}

func parseWeekday(value string) (int, bool) {
	days := map[string]int{"sun": 0, "вс": 0, "mon": 1, "пн": 1, "tue": 2, "вт": 2, "wed": 3, "ср": 3, "thu": 4, "чт": 4, "fri": 5, "пт": 5, "sat": 6, "сб": 6}
	day, ok := days[strings.ToLower(strings.TrimSpace(value))]
	return day, ok
}

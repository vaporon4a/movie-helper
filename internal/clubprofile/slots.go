package clubprofile

import "time"

func WeekKey(slot time.Time, zone string) (string, error) {
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return "", err
	}
	local := slot.In(loc)
	return local.AddDate(0, 0, -(int(local.Weekday())+6)%7).Format(time.DateOnly), nil
}

// ResearchSlot respects frozen roles, including newly added ordinary slots in a frozen week.
func ResearchSlot(schedules []Schedule, sc Schedule, slot time.Time) bool {
	week, err := WeekKey(slot, sc.Zone)
	if err != nil || !sc.Enabled {
		return false
	}
	for _, s := range schedules {
		if _, exists := s.WeekRoles[week]; exists {
			return sc.WeekRoles[week] == "research"
		}
	}
	return ResearchRoles(schedules)[sc.ID]
}

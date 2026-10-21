package clubprofile

import "time"

// WeeklySlot preserves the first matching wall-clock occurrence across DST changes.
func WeeklySlot(now time.Time, weekday int, clock, zone string) (time.Time, error) {
	loc, err := time.LoadLocation(zone)
	if err != nil || weekday < 0 || weekday > 6 {
		return time.Time{}, ErrInvalid
	}
	parsed, err := time.Parse("15:04", clock)
	if err != nil {
		return time.Time{}, err
	}
	local := now.In(loc)
	target := local.AddDate(0, 0, weekday-int(local.Weekday()))
	y, m, d := target.Date()
	anchor := time.Date(y, m, d, 12, 0, 0, 0, loc)
	want := parsed.Hour()*60 + parsed.Minute()
	for at := anchor.Add(-18 * time.Hour); !at.After(anchor.Add(18 * time.Hour)); at = at.Add(time.Minute) {
		v := at.In(loc)
		vy, vm, vd := v.Date()
		if vy == y && vm == m && vd == d && v.Hour()*60+v.Minute() >= want {
			return at, nil
		}
	}
	return time.Time{}, ErrInvalid
}

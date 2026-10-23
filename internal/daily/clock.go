package daily

import (
	"errors"
	"time"
)

func Slot(now time.Time, clock, zone string) (time.Time, error) {
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return time.Time{}, err
	}
	parsed, err := time.Parse("15:04", clock)
	if err != nil {
		return time.Time{}, err
	}
	local := now.In(loc)
	y, m, d := local.Date()
	anchor := time.Date(y, m, d, 12, 0, 0, 0, loc)
	want := parsed.Hour()*60 + parsed.Minute()
	for t := anchor.Add(-18 * time.Hour); !t.After(anchor.Add(18 * time.Hour)); t = t.Add(time.Minute) {
		l := t.In(loc)
		ly, lm, ld := l.Date()
		if ly == y && lm == m && ld == d && l.Hour()*60+l.Minute() >= want {
			return t, nil
		}
	}
	return time.Time{}, errors.New("no slot on local day")
}
func UpcomingSlots(sc Schedule, now time.Time, days int) []time.Time {
	if !sc.Enabled {
		return nil
	}
	loc, err := time.LoadLocation(sc.Zone)
	if err != nil {
		return nil
	}
	var out []time.Time
	local := now.In(loc)
	for day := 0; day <= days; day++ {
		at, e := Slot(local.AddDate(0, 0, day), sc.Clock, sc.Zone)
		if e == nil && at.After(now) && at.Unix() > sc.Effective && !at.After(local.AddDate(0, 0, days)) {
			out = append(out, at)
		}
	}
	return out
}

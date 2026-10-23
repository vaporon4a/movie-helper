package storage

import (
	"testing"
	"time"

	"github.com/vaporon4a/movie-helper/internal/agenda"
	"github.com/vaporon4a/movie-helper/internal/clubprofile"
)

func TestAgendaIsReadOnlyAndUsesChatTimeAndSeparatesMeetingDate(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	setup(t, s, -1)
	must(t, s.SetZone(ctx, 123, -1, "Asia/Novosibirsk", testNow))
	must(t, s.SetClubSchedule(ctx, 124, -1, 5, 2, "19:23", testNow))
	must(t, s.SetProfileSchedule(ctx, 125, clubprofile.Schedule{ChatID: -1, Topic: clubprofile.Genre, Weekday: int(testNow.Weekday()), Clock: "18:00", Enabled: true, Effective: testNow}))
	before := s.db.Stats()
	var count int
	must(t, s.db.QueryRowContext(ctx, "SELECT count(*) FROM club_profile_polls").Scan(&count))
	view := agenda.New(s).Read(ctx, -1, testNow)
	if view.Zone != "Asia/Novosibirsk" || view.Meeting == "" || len(view.Events) == 0 || len(view.Unavailable) != 0 {
		t.Fatal(view)
	}
	for _, e := range view.Events {
		if e.At.After(testNow.Add(7 * 24 * time.Hour)) {
			t.Fatal("horizon", e)
		}
		if e.Kind == "club" && e.Zone != "Asia/Novosibirsk" {
			t.Fatal("wrong zone", e)
		}
	}
	var after int
	must(t, s.db.QueryRowContext(ctx, "SELECT count(*) FROM club_profile_polls").Scan(&after))
	if after != count || before.InUse != s.db.Stats().InUse {
		t.Fatal("agenda reserved a poll or leaked connection")
	}
}

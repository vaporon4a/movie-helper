package storage

import (
	"database/sql"
	"testing"
	"time"

	"github.com/vaporon4a/movie-helper/internal/clubprofile"
)

func TestProfileScheduleEditCannotDuplicateSameDayAndPauseCancelsResearch(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	must(t, s.EnsureChat(ctx, -1))
	now := testNow
	must(t, s.SetZone(ctx, 900, -1, "UTC", now))
	sc := clubprofile.Schedule{ChatID: -1, Topic: clubprofile.Genre, Weekday: int(now.Weekday()), Clock: now.Format("15:04"), Enabled: true, Effective: now.Add(-time.Hour)}
	must(t, s.SetProfileSchedule(ctx, 901, sc))
	schedules, err := s.ProfileSchedules(ctx, -1)
	must(t, err)
	sc = schedules[0]
	q, _ := clubprofile.ReviewQuestion(clubprofile.Overall, "X")
	q.Topic = clubprofile.Genre
	q.Intent = clubprofile.Interest
	must(t, s.ReserveProfileSlot(ctx, sc, q, now, now))
	sc.Clock = now.Add(time.Hour).Format("15:04")
	sc.Effective = now.Add(time.Minute)
	must(t, s.SetProfileSchedule(ctx, 902, sc))
	if err = s.ReserveProfileSlot(ctx, sc, q, now.Add(time.Hour), now.Add(time.Hour)); err == nil {
		t.Fatal("same weekday repeated after clock edit")
	}
	// A post-watch child is owned by its underlying schedule, not its post_watch topic.
	_, err = s.db.ExecContext(ctx, "UPDATE club_profile_polls SET topic='post_watch',state='planned'")
	must(t, err)
	must(t, s.PauseProfileSchedules(ctx, 903, -1, clubprofile.Genre, now))
	ps, err := s.ProfilePolls(ctx, -1, clubprofile.Planned)
	must(t, err)
	if len(ps) != 0 {
		t.Fatal("research child survived pause", ps)
	}
}

func TestProfileResearchDayLimitSpansSessions(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	must(t, s.EnsureChat(ctx, -1))
	now := testNow
	d := addProfileWatch(t, s, -1, 920, now.Format(time.DateOnly), now)
	_, err := s.db.ExecContext(ctx, "INSERT INTO club_poll_batches(chat_id,session_id,occurrence,slot_at) VALUES(-1,?,'old',?)", d.WatchID, now.Unix())
	must(t, err)
	must(t, s.transaction(ctx, nil, func(tx *sql.Tx) error {
		used, e := researchDayUsed(ctx, tx, -1, "UTC", now.Add(time.Hour))
		if e != nil {
			return e
		}
		if !used {
			t.Fatal("second research batch allowed same day")
		}
		used, e = researchDayUsed(ctx, tx, -1, "UTC", now.AddDate(0, 0, 1))
		if used {
			t.Fatal("next day blocked")
		}
		return e
	}))
}

func TestProfileCleanupExpiresWithoutDeletingOrChangingFinal(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	must(t, s.EnsureChat(ctx, -1))
	id := profilePoll(t, s, -1)
	ok, err := s.ClaimProfilePoll(ctx, id, clubprofile.Planned, clubprofile.Creating)
	must(t, err)
	if !ok {
		t.Fatal("claim")
	}
	must(t, s.OpenProfilePoll(ctx, id, "expired", 42, testNow))
	_, err = s.CompleteProfilePoll(ctx, clubprofile.PollResult{PollID: "expired", Counts: []int{0, 0, 0, 0, 3, 0, 0}, Total: new(3), Closed: true, Source: clubprofile.SourceUpdate, ReceivedAt: testNow.Add(49 * time.Hour)})
	must(t, err)
	jobs, err := s.DueProfileCleanup(ctx, -1, testNow.Add(49*time.Hour))
	must(t, err)
	if len(jobs) != 0 {
		t.Fatal(jobs)
	}
	status, err := s.ProfileStatus(ctx, -1)
	must(t, err)
	if len(status.CleanupIssues) != 1 || status.CleanupIssues[0].State != clubprofile.CleanupExpired {
		t.Fatal(status)
	}
}

func TestRewatchIdentityDoesNotEraseEnrichment(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	must(t, s.EnsureChat(ctx, -1))
	addProfileWatch(t, s, -1, 930, testNow.Format(time.DateOnly), testNow)
	f, err := s.CachedProfileFilm(ctx, 603)
	must(t, err)
	f.Metadata = clubprofile.FilmMetadata{Complete: true, Keywords: []clubprofile.FilmTag{{ID: 1, Name: "simulation"}}}
	must(t, s.SaveFilmEnrichment(ctx, 603, &f, testNow))
	addProfileWatch(t, s, -1, 931, testNow.AddDate(0, 0, 1).Format(time.DateOnly), testNow.AddDate(0, 0, 1))
	f, err = s.CachedProfileFilm(ctx, 603)
	must(t, err)
	if !f.Metadata.Complete || len(f.Metadata.Keywords) != 1 {
		t.Fatal(f)
	}
}

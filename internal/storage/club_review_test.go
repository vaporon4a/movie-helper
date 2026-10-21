package storage

import (
	"fmt"
	"testing"
	"time"

	"github.com/vaporon4a/movie-helper/internal/clubprofile"
)

func addProfileWatch(t *testing.T, s *Store, chat, op int64, date string, now time.Time) clubprofile.WatchDraft {
	t.Helper()
	in, err := clubprofile.ParseWatchInput("https://www.themoviedb.org/movie/603 | "+date, "UTC", now)
	must(t, err)
	d, err := s.CreateWatchDraft(t.Context(), op, chat, in, now)
	must(t, err)
	f := clubprofile.Film{TMDBID: 603, Title: "Матрица", Year: 1999, Source: "tmdb", FetchedAt: now}
	d, err = s.FinishWatchResolution(t.Context(), d, []clubprofile.Film{f}, "", now)
	must(t, err)
	return d
}

func TestWatchIdentityDuplicatesHistoryAndEqualCorrections(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	must(t, s.EnsureChat(ctx, -1))
	must(t, s.EnsureChat(ctx, -2))
	d := addProfileWatch(t, s, -1, 91, testNow.Format(time.DateOnly), testNow)
	duplicate := addProfileWatch(t, s, -1, 92, testNow.Format(time.DateOnly), testNow)
	if d.WatchID != duplicate.WatchID {
		t.Fatal("duplicate screening")
	}
	rewatch := addProfileWatch(t, s, -1, 93, testNow.AddDate(0, 0, -1).Format(time.DateOnly), testNow)
	if d.WatchID == rewatch.WatchID {
		t.Fatal("rewatch merged")
	}
	addProfileWatch(t, s, -1, 94, "2020", testNow)
	addProfileWatch(t, s, -1, 95, "unknown", testNow)
	ss, err := s.ProfileSessions(ctx, -1)
	must(t, err)
	if len(ss) != 2 {
		t.Fatal("retrospective spawned survey", ss)
	}
	if err = s.ChangeProfileWatch(ctx, 96, -2, d.WatchID, "annul", "", testNow); err == nil {
		t.Fatal("cross-chat mutation")
	}
	must(t, s.ChangeProfileWatch(ctx, 97, -1, d.WatchID, "annul", "", testNow))
	ss, err = s.ProfileSessions(ctx, -1)
	must(t, err)
	if ss[0].State != "cancelled" {
		t.Fatal(ss)
	}
	var n int
	must(t, s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM club_watch_audit WHERE watch_id=?", d.WatchID).Scan(&n))
	if n != 2 {
		t.Fatal(n)
	}
}

func TestResearchBatchSlotsAndCompleteSeries(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	chat := int64(-1)
	now := time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)
	must(t, s.EnsureChat(ctx, chat))
	must(t, s.SetZone(ctx, 700, chat, "UTC", now.Add(-time.Hour)))
	d := addProfileWatch(t, s, chat, 701, "2026-10-18", now)
	schedules := []clubprofile.Schedule{{ChatID: chat, Topic: clubprofile.Genre, Weekday: 1, Clock: "09:00", Enabled: true, Effective: now.Add(-time.Hour)}, {ChatID: chat, Topic: clubprofile.Reference, Weekday: 1, Clock: "12:00", Enabled: true, Effective: now.Add(-time.Hour)}, {ChatID: chat, Topic: clubprofile.Genre, Weekday: 3, Clock: "09:00", Enabled: true, Effective: now.Add(-time.Hour)}, {ChatID: chat, Topic: clubprofile.Reference, Weekday: 3, Clock: "12:00", Enabled: true, Effective: now.Add(-time.Hour)}}
	for i, sc := range schedules {
		must(t, s.SetProfileSchedule(ctx, int64(710+i), sc))
	}
	schedules, err := s.ProfileSchedules(ctx, chat)
	must(t, err)
	q := clubprofile.Question{Topic: clubprofile.Genre, Intent: clubprofile.Interest, Scale: "single-interest-v1", Version: clubprofile.QuestionV1, Text: "Интерес?", Anonymous: true, Options: []clubprofile.Option{{Key: "a", Text: "Да", Substantive: true}, {Key: "b", Text: "Нет", Substantive: true}}}
	reserve := func(sc clubprofile.Schedule, slot time.Time) {
		t.Helper()
		q.Topic = sc.Topic
		must(t, s.ReserveProfileSlot(ctx, sc, q, slot, slot))
		if err = s.ReserveProfileSlot(ctx, sc, q, slot, slot); err == nil {
			t.Fatal("duplicate slot")
		}
	}
	reserve(schedules[0], now)
	ps, err := s.ProfilePolls(ctx, chat, clubprofile.Planned)
	must(t, err)
	if len(ps) != 2 || ps[0].Question.Aspect != clubprofile.Overall || ps[1].Question.Aspect != clubprofile.Pace {
		t.Fatal(ps)
	}
	finish := func(at time.Time) {
		t.Helper()
		polls, e := s.ProfilePolls(ctx, chat, clubprofile.Planned)
		must(t, e)
		for _, p := range polls {
			ok, e := s.ClaimProfilePoll(ctx, p.ID, clubprofile.Planned, clubprofile.Creating)
			must(t, e)
			if !ok {
				t.Fatalf("claim %d", p.ID)
			}
			pid := fmt.Sprint(p.ID)
			must(t, s.OpenProfilePoll(ctx, p.ID, pid, int(p.ID), at))
			counts := make([]int, len(p.Question.Options))
			counts[0] = 3
			_, e = s.CompleteProfilePoll(ctx, clubprofile.PollResult{PollID: pid, Counts: counts, Total: new(3), Closed: true, Source: clubprofile.SourceStop, ReceivedAt: at.Add(24 * time.Hour)})
			must(t, e)
		}
		must(t, s.AdvanceProfileReviews(ctx, at.Add(24*time.Hour)))
	}
	finish(now)
	reserve(schedules[1], now.Add(3*time.Hour))
	ps, err = s.ProfilePolls(ctx, chat, clubprofile.Planned)
	must(t, err)
	if len(ps) != 1 || ps[0].Question.Topic != clubprofile.Reference || ps[0].SessionID != 0 {
		t.Fatal("ordinary reference lost", ps)
	}
	finish(now.Add(3 * time.Hour))
	reserve(schedules[2], now.AddDate(0, 0, 2))
	ps, err = s.ProfilePolls(ctx, chat, clubprofile.Planned)
	must(t, err)
	if len(ps) != 1 || ps[0].Question.Topic != clubprofile.Genre {
		t.Fatal("ordinary genre lost", ps)
	}
	finish(now.AddDate(0, 0, 2))
	reserve(schedules[3], now.AddDate(0, 0, 2).Add(3*time.Hour))
	finish(now.AddDate(0, 0, 2).Add(3 * time.Hour))
	reserve(schedules[0], now.AddDate(0, 0, 7))
	finish(now.AddDate(0, 0, 7))
	ss, err := s.ProfileSessions(ctx, chat)
	must(t, err)
	if len(ss) != 1 || ss[0].WatchID != d.WatchID || ss[0].State != "completed_sufficient" {
		t.Fatal(ss)
	}
	ps, err = s.ProfilePolls(ctx, chat, "")
	must(t, err)
	post := 0
	for _, p := range ps {
		if p.Question.Topic == clubprofile.PostWatch {
			post++
		}
	}
	if post != 5 {
		t.Fatal("unexpected questions", post)
	}
	jobs, err := s.DueProfileCleanup(ctx, -1, now.AddDate(0, 0, 8))
	must(t, err)
	if len(jobs) != 7 {
		t.Fatal("lost cleanup", jobs)
	}
}

func TestUncertainChildBlocksSiblingAndPreservesBudget(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	must(t, s.EnsureChat(ctx, -1))
	now := testNow
	addProfileWatch(t, s, -1, 800, now.Format(time.DateOnly), now)
	// Reserve a concrete research batch without depending on weekday role selection.
	var session int64
	must(t, s.db.QueryRowContext(ctx, "SELECT id FROM club_review_sessions").Scan(&session))
	_, err := s.db.ExecContext(ctx, "UPDATE club_review_sessions SET state='researching' WHERE id=?", session)
	must(t, err)
	_, err = s.db.ExecContext(ctx, "INSERT INTO club_poll_batches(chat_id,session_id,occurrence,slot_at) VALUES(-1,?,'test',?)", session, now.Unix())
	must(t, err)
	for i, a := range []clubprofile.Aspect{clubprofile.Overall, clubprofile.Pace} {
		q, e := clubprofile.ReviewQuestion(a, "Матрица")
		must(t, e)
		_, e = s.PlanProfilePoll(ctx, new(int64(810+i)), clubprofile.Poll{ChatID: -1, SessionID: session, BatchID: 1, Position: i, Question: q, SlotAt: now, Duration: 24 * time.Hour})
		must(t, e)
	}
	ok, err := s.ClaimProfilePoll(ctx, 1, clubprofile.Planned, clubprofile.Creating)
	must(t, err)
	if !ok {
		t.Fatal("first claim")
	}
	must(t, s.RecoverProfilePolls(ctx, now))
	ok, err = s.ClaimProfilePoll(ctx, 2, clubprofile.Planned, clubprofile.Creating)
	must(t, err)
	if ok {
		t.Fatal("sent sibling after unknown")
	}
	must(t, s.AdvanceProfileReviews(ctx, now))
	ss, err := s.ProfileSessions(ctx, -1)
	must(t, err)
	if ss[0].State != "blocked" {
		t.Fatal(ss)
	}
	must(t, s.ResolveProfilePoll(ctx, 850, -1, 1, "cancel", now))
	ps, err := s.ProfilePolls(ctx, -1, "")
	must(t, err)
	if !ps[0].DeliveryCounted {
		t.Fatal("lost uncertain send budget")
	}
	ss, err = s.ProfileSessions(ctx, -1)
	must(t, err)
	if ss[0].State != "partial" {
		t.Fatal(ss)
	}
}

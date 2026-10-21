package storage

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/vaporon4a/movie-helper/internal/clubprofile"
)

func profilePoll(t *testing.T, s *Store, chat int64) int64 {
	t.Helper()
	q, err := clubprofile.ReviewQuestion(clubprofile.Overall, "Матрица")
	must(t, err)
	id, err := s.PlanProfilePoll(t.Context(), nil, clubprofile.Poll{ChatID: chat, Question: q, SlotAt: testNow, Duration: time.Hour})
	must(t, err)
	return id
}

func TestProfileFinalizationAtomicAndIdempotent(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	must(t, s.EnsureChat(ctx, -1))
	id := profilePoll(t, s, -1)
	claimed, err := s.ClaimProfilePoll(ctx, id, clubprofile.Planned, clubprofile.Creating)
	must(t, err)
	if !claimed {
		t.Fatal("not claimed")
	}
	must(t, s.OpenProfilePoll(ctx, id, "poll", 42, testNow))
	r := clubprofile.PollResult{PollID: "poll", Counts: []int{1, 0, 1, 0, 3, 2, 0}, Total: new(7), Closed: true, Source: clubprofile.SourceStop, ReceivedAt: testNow.Add(time.Hour)}
	_, err = s.db.ExecContext(ctx, `CREATE TRIGGER reject_cleanup BEFORE INSERT ON club_poll_cleanup BEGIN SELECT RAISE(ABORT,'test'); END`)
	must(t, err)
	if _, err = s.CompleteProfilePoll(ctx, r); err == nil {
		t.Fatal("missing cleanup failure")
	}
	ps, err := s.ProfilePolls(ctx, -1, clubprofile.Open)
	must(t, err)
	if len(ps) != 1 || ps[0].Result != nil {
		t.Fatal("partial commit", ps)
	}
	_, err = s.db.ExecContext(ctx, "DROP TRIGGER reject_cleanup")
	must(t, err)
	ok, err := s.CompleteProfilePoll(ctx, r)
	must(t, err)
	if !ok {
		t.Fatal("not finalized")
	}
	ok, err = s.CompleteProfilePoll(ctx, r)
	must(t, err)
	if ok {
		t.Fatal("duplicate applied")
	}
	var n int
	must(t, s.db.QueryRowContext(ctx, "SELECT count(*) FROM club_poll_cleanup WHERE poll_id=?", id).Scan(&n))
	if n != 1 {
		t.Fatal(n)
	}
	ps, err = s.ProfilePolls(ctx, -2, "")
	must(t, err)
	if len(ps) != 0 {
		t.Fatal("cross-chat read")
	}
}

func TestProfileConcurrentClaimAndRecovery(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	must(t, s.EnsureChat(ctx, -1))
	id := profilePoll(t, s, -1)
	results := make(chan bool, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			ok, err := s.ClaimProfilePoll(ctx, id, clubprofile.Planned, clubprofile.Creating)
			if err != nil {
				t.Error(err)
			}
			results <- ok
		})
	}
	wg.Wait()
	close(results)
	n := 0
	for ok := range results {
		if ok {
			n++
		}
	}
	if n != 1 {
		t.Fatal(n)
	}
	must(t, s.RecoverProfilePolls(ctx, testNow))
	ps, err := s.ProfilePolls(ctx, -1, clubprofile.Unknown)
	must(t, err)
	if len(ps) != 1 {
		t.Fatal(ps)
	}
	if err = s.ResolveProfilePoll(ctx, 99, -1, id, "retry", testNow); !errors.Is(err, clubprofile.ErrConflict) {
		t.Fatal("unknown resent", err)
	}
}

func TestProfileUpgradePreservesEvidenceAndSchedule(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	m, err := s.migrator()
	must(t, err)
	_, err = m.DownTo(ctx, 18)
	must(t, err)
	must(t, s.EnsureChat(ctx, -1))
	must(t, s.SetZone(ctx, 1, -1, "Asia/Novosibirsk", testNow))
	_, err = s.db.ExecContext(ctx, "INSERT INTO movie_poll_schedules(chat_id,feature,weekday,clock,enabled,effective) VALUES(-1,'genre',1,'09:00',0,?)", testNow.Unix())
	must(t, err)
	id := seedLegacyProfileRound(t, s, "open")
	_, err = m.Up(ctx)
	must(t, err)
	must(t, s.MigrateClubProfile(ctx, testNow))
	schedules, err := s.ProfileSchedules(ctx, -1)
	must(t, err)
	if len(schedules) != 1 || schedules[0].Enabled || schedules[0].Clock != "09:00" || schedules[0].Zone != "Asia/Novosibirsk" {
		t.Fatal(schedules)
	}
	ps, err := s.ProfilePolls(ctx, -1, clubprofile.Open)
	must(t, err)
	if len(ps) != 1 || ps[0].Question.Text != "" || ps[0].Question.Intent != clubprofile.LegacyInterest || ps[0].Result.Total != nil {
		t.Fatal(ps)
	}
	counts := make([]int, 3)
	counts[2] = 4
	ok, err := s.CompleteProfilePoll(ctx, clubprofile.PollResult{PollID: "old", Counts: counts, Total: new(4), Closed: true, Source: clubprofile.SourceUpdate, ReceivedAt: testNow.Add(time.Hour)})
	must(t, err)
	if !ok {
		t.Fatal("legacy not closed")
	}
	var state, code string
	var votes int
	must(t, s.db.QueryRowContext(ctx, "SELECT state,error_code FROM movie_rounds WHERE id=?", id).Scan(&state, &code))
	must(t, s.db.QueryRowContext(ctx, "SELECT votes FROM movie_poll_options WHERE round_id=? AND position=2", id).Scan(&votes))
	if state != "cancelled" || code != "profile_collected" || votes != 4 {
		t.Fatal(state, code, votes)
	}
	must(t, s.MigrateClubProfile(ctx, testNow))
	ps, err = s.ProfilePolls(ctx, -1, "")
	must(t, err)
	if len(ps) != 1 {
		t.Fatal("duplicate import")
	}
	var n int
	must(t, s.db.QueryRowContext(ctx, "SELECT count(*) FROM club_poll_cleanup").Scan(&n))
	if n != 0 {
		t.Fatal("legacy cleanup")
	}
}

func TestProfileMigrationRollback(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	must(t, s.EnsureChat(ctx, -1))
	id := seedLegacyProfileRound(t, s, "open")
	var err error
	_, err = s.db.ExecContext(ctx, "UPDATE movie_poll_options SET metadata_json='bad' WHERE round_id=? AND position=0", id)
	must(t, err)
	if err = s.MigrateClubProfile(ctx, testNow); err == nil {
		t.Fatal("corrupt import ignored")
	}
	for _, table := range []string{"club_profile_migration", "club_profile_polls", "club_profile_schedules"} {
		var n int
		must(t, s.db.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&n))
		if n != 0 {
			t.Fatal(table, n)
		}
	}
}

func seedLegacyProfileRound(t *testing.T, s *Store, state string) int64 {
	t.Helper()
	result, err := s.db.ExecContext(t.Context(), `INSERT INTO movie_rounds(chat_id,feature,slot_at,state,telegram_poll_id,poll_message_id,opened_at,closes_at,result_text) VALUES(-1,'genre',?,?,'old',10,?,?,'obsolete presentation')`, testNow.Unix(), state, testNow.Unix(), testNow.Add(time.Hour).Unix())
	must(t, err)
	id, err := result.LastInsertId()
	must(t, err)
	for i, label := range []string{"Боевик", "Комедия", "Драма"} {
		_, err = s.db.ExecContext(t.Context(), `INSERT INTO movie_poll_options(round_id,position,option_kind,provider_id,label,votes,metadata_json) VALUES(?,?,'genre',?,?,?,?)`, id, i, []int{28, 35, 18}[i], label, i, `{"genre_ids":[18]}`)
		must(t, err)
	}
	return id
}

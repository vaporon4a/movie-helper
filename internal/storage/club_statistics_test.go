package storage

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/vaporon4a/movie-helper/internal/clubprofile"
	"github.com/vaporon4a/movie-helper/internal/daily"
)

func statisticsSetup(t *testing.T, s *Store) (clubprofile.StatisticsSettings, clubprofile.StatisticsSnapshot, time.Time) {
	t.Helper()
	ctx := t.Context()
	slot := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	must(t, s.EnsureChat(ctx, -1))
	must(t, s.SetZone(ctx, 12000, -1, "UTC", slot.Add(-time.Hour)))
	sc, err := s.StatisticsSettings(ctx, -1)
	must(t, err)
	if sc.Enabled || sc.Weekday != 5 || sc.Clock != "09:00" {
		t.Fatal(sc)
	}
	sc.Enabled = true
	must(t, s.SetStatisticsSettings(ctx, sc, slot.Add(-time.Hour)))
	sc, err = s.StatisticsSettings(ctx, -1)
	must(t, err)
	snap := clubprofile.StatisticsSnapshot{ChatID: -1, Zone: "UTC", Version: clubprofile.StatisticsVersion, AsOf: slot, Period: clubprofile.DigestPeriod(slot), Claims: []clubprofile.StatisticsClaim{{ID: "claim", Text: "Проверенный итог", PollID: 1, ClosedAt: slot.Add(-24 * time.Hour)}}}
	return sc, snap, slot
}
func TestStatisticsDeliveryAtomicIdempotentAndUnknown(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	sc, snap, slot := statisticsSetup(t, s)
	must(t, s.ReserveStatistics(ctx, sc, snap, slot))
	must(t, s.ReserveStatistics(ctx, sc, snap, slot))
	due, err := s.StatisticsDue(ctx, slot)
	must(t, err)
	if len(due) != 1 {
		t.Fatal(due)
	}
	id := due[0].ID
	claimed, err := s.ClaimStatistics(ctx, id, slot)
	must(t, err)
	if !claimed {
		t.Fatal("not claimed")
	}
	claimed, err = s.ClaimStatistics(ctx, id, slot)
	must(t, err)
	if claimed {
		t.Fatal("duplicate claim")
	}
	must(t, s.RecoverStatistics(ctx))
	due, err = s.StatisticsDue(ctx, slot)
	must(t, err)
	if len(due) != 0 {
		t.Fatal("unknown auto-retried")
	}
	var state string
	must(t, s.db.QueryRowContext(ctx, "SELECT state FROM club_statistics_deliveries WHERE id=?", id).Scan(&state))
	if state != "unknown" {
		t.Fatal(state)
	}
	// Changing the day cannot produce a second delivery in the same ISO week.
	must(t, s.ReserveStatistics(ctx, sc, snap, slot.AddDate(0, 0, 1)))
	var n int
	must(t, s.db.QueryRowContext(ctx, "SELECT count(*) FROM club_statistics_snapshots").Scan(&n))
	if n != 1 {
		t.Fatal(n)
	}
	// The same evidence next week is silently skipped, even after an uncertain send.
	snap.Period = clubprofile.DigestPeriod(slot.AddDate(0, 0, 7))
	snap.AsOf = slot.AddDate(0, 0, 7)
	must(t, s.ReserveStatistics(ctx, sc, snap, slot.AddDate(0, 0, 7)))
	must(t, s.db.QueryRowContext(ctx, "SELECT state FROM club_statistics_deliveries ORDER BY id DESC LIMIT 1").Scan(&state))
	if state != "skipped" {
		t.Fatal(state)
	}
}
func TestStatisticsCASPauseInactiveAndDeadline(t *testing.T) {
	for _, action := range []string{"pause", "inactive", "zone", "deadline"} {
		t.Run(action, func(t *testing.T) {
			s := testStore(t)
			ctx := t.Context()
			sc, snap, slot := statisticsSetup(t, s)
			must(t, s.ReserveStatistics(ctx, sc, snap, slot))
			due, err := s.StatisticsDue(ctx, slot)
			must(t, err)
			switch action {
			case "pause":
				sc.Enabled = false
				must(t, s.SetStatisticsSettings(ctx, sc, slot))
				if !errors.Is(s.SetStatisticsSettings(ctx, sc, slot), clubprofile.ErrConflict) {
					t.Fatal("stale CAS applied")
				}
			case "inactive":
				_, err = s.db.ExecContext(ctx, "UPDATE chats SET active=0 WHERE chat_id=-1")
				must(t, err)
			case "zone":
				_, err = s.db.ExecContext(ctx, "UPDATE chats SET zone='Asia/Novosibirsk' WHERE chat_id=-1")
				must(t, err)
			case "deadline":
				slot = slot.Add(24 * time.Hour)
			}
			claimed, err := s.ClaimStatistics(ctx, due[0].ID, slot)
			must(t, err)
			if claimed {
				t.Fatal("invalid delivery claimed")
			}
		})
	}
}
func TestStatisticsSnapshotIsolationAndReplay(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	setup(t, s, -1)
	setup(t, s, -2)
	if _, err := s.StatisticsInput(ctx, 0); !errors.Is(err, clubprofile.ErrInvalid) {
		t.Fatal("zero chat allowed", err)
	}
	d := addProfileWatch(t, s, -1, 1500, "2026-09-22", testNow)
	sessions, err := s.ProfileSessions(ctx, -1)
	must(t, err)
	q, err := clubprofile.ReviewQuestion(clubprofile.Overall, "Матрица")
	must(t, err)
	id, err := s.PlanProfilePoll(ctx, nil, clubprofile.Poll{ChatID: -1, SessionID: sessions[0].ID, Question: q, SlotAt: testNow, Duration: time.Hour})
	must(t, err)
	_, err = s.db.ExecContext(ctx, "UPDATE club_review_sessions SET state='researching' WHERE id=?", sessions[0].ID)
	must(t, err)
	claimed, err := s.ClaimProfilePoll(ctx, id, clubprofile.Planned, clubprofile.Creating)
	must(t, err)
	if !claimed {
		t.Fatal("not claimed")
	}
	must(t, s.OpenProfilePoll(ctx, id, "statistics-final", 15, testNow))
	_, err = s.CompleteProfilePoll(ctx, clubprofile.PollResult{PollID: "statistics-final", Counts: []int{2, 1, 0, 2, 0, 1, 0}, Total: new(6), Closed: true, Source: clubprofile.SourceStop, ReceivedAt: testNow.Add(time.Hour)})
	must(t, err)
	in, err := s.StatisticsInput(ctx, -1)
	must(t, err)
	out := clubprofile.BuildStatistics(in, clubprofile.StatisticsPeriod{}, testNow.Add(2*time.Hour))
	if len(out.Claims) != 1 || out.Answers != 6 || !strings.Contains(out.Claims[0].Text, "оценок: 5") {
		t.Fatal(out)
	}
	other, err := s.StatisticsInput(ctx, -2)
	must(t, err)
	if len(other.Watches) != 0 || len(other.Polls) != 0 {
		t.Fatal("chat leak")
	}
	must(t, s.ChangeProfileWatch(ctx, 1600, -1, d.WatchID, "annul", "", testNow))
	in, err = s.StatisticsInput(ctx, -1)
	must(t, err)
	if len(clubprofile.BuildStatistics(in, clubprofile.StatisticsPeriod{}, testNow.Add(2*time.Hour)).Claims) != 0 {
		t.Fatal("cancelled film retained")
	}
}

type statisticsTestSender struct {
	calls int
	fail  error
	texts []string
}

func (s *statisticsTestSender) SendReminder(_ context.Context, _ int64, text string) (int, error) {
	s.calls++
	s.texts = append(s.texts, text)
	return 42, s.fail
}
func TestStatisticsCoordinatorRetryFrozenPayloadAndAllowlist(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	sc, snap, slot := statisticsSetup(t, s)
	must(t, s.ReserveStatistics(ctx, sc, snap, slot))
	sender := &statisticsTestSender{fail: &daily.SendError{Kind: "retry", After: time.Minute}}
	now := slot
	c := clubprofile.StatisticsCoordinator{Store: s, Reader: s, Sender: sender, Allowed: map[int64]bool{}, Now: func() time.Time { return now }, Log: slog.New(slog.DiscardHandler)}
	must(t, c.Tick(ctx))
	if sender.calls != 0 {
		t.Fatal("allowlist bypassed")
	}
	c.Allowed[-1] = true
	must(t, c.Tick(ctx))
	must(t, c.Tick(ctx))
	if sender.calls != 1 {
		t.Fatal("retry-after ignored")
	}
	now = now.Add(time.Minute)
	sender.fail = nil
	must(t, c.Tick(ctx))
	must(t, c.Tick(ctx))
	if sender.calls != 2 || sender.texts[0] != sender.texts[1] {
		t.Fatal("retry changed payload or duplicated", sender)
	}
}

func TestStatisticsMigration31PreservesExistingState(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	setup(t, s, -1)
	d := addProfileWatch(t, s, -1, 1800, testNow.Format(time.DateOnly), testNow)
	m, err := s.migrator()
	must(t, err)
	_, err = m.DownTo(ctx, 31)
	must(t, err)
	_, err = m.Up(ctx)
	must(t, err)
	in, err := s.StatisticsInput(ctx, -1)
	must(t, err)
	if len(in.Watches) != 1 || in.Watches[0].ID != d.WatchID || in.Watches[0].Revision != 1 {
		t.Fatal("migration changed history", in)
	}
	settings, err := s.StatisticsSettings(ctx, -1)
	must(t, err)
	if settings.Enabled {
		t.Fatal("migration enabled broadcast")
	}
}
func TestStatisticsReservationRollsBackAndArchiveIsImmutable(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	sc, snap, slot := statisticsSetup(t, s)
	_, err := s.db.ExecContext(ctx, `CREATE TRIGGER fail_statistics BEFORE INSERT ON club_statistics_claims BEGIN SELECT RAISE(ABORT,'test'); END`)
	must(t, err)
	if err = s.ReserveStatistics(ctx, sc, snap, slot); err == nil {
		t.Fatal("missing injected error")
	}
	reports, err := s.StatisticsReports(ctx, -1)
	must(t, err)
	if len(reports) != 0 {
		t.Fatal("partial snapshot committed")
	}
	_, err = s.db.ExecContext(ctx, "DROP TRIGGER fail_statistics")
	must(t, err)
	must(t, s.ReserveStatistics(ctx, sc, snap, slot))
	snap.Claims[0].Text = "Changed after reservation"
	reports, err = s.StatisticsReports(ctx, -1)
	must(t, err)
	if len(reports) != 1 || strings.Contains(reports[0].Text, "Changed") {
		t.Fatal("mutable snapshot")
	}
	other, err := s.StatisticsReports(ctx, -2)
	must(t, err)
	if len(other) != 0 {
		t.Fatal("archive leaked")
	}
}

func TestStatisticsTimezoneShiftDoesNotOverlapDeliveredWindow(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	sc, snap, slot := statisticsSetup(t, s)
	must(t, s.ReserveStatistics(ctx, sc, snap, slot))
	due, err := s.StatisticsDue(ctx, slot)
	must(t, err)
	_, err = s.ClaimStatistics(ctx, due[0].ID, slot)
	must(t, err)
	must(t, s.FinishStatistics(ctx, due[0].ID, "sent", 42, time.Time{}, ""))
	_, err = s.db.ExecContext(ctx, "UPDATE chats SET zone='Asia/Novosibirsk' WHERE chat_id=-1")
	must(t, err)
	sc, err = s.StatisticsSettings(ctx, -1)
	must(t, err)
	loc, err := time.LoadLocation(sc.Zone)
	must(t, err)
	next := time.Date(2026, 11, 20, 9, 0, 0, 0, loc)
	snap.Zone = sc.Zone
	snap.Period = clubprofile.DigestPeriod(next)
	snap.AsOf = next
	snap.Claims[0].ID = "new"
	must(t, s.ReserveStatistics(ctx, sc, snap, next))
	reports, err := s.StatisticsReports(ctx, -1)
	must(t, err)
	if len(reports) != 1 {
		t.Fatal("timezone overlapped published reporting window")
	}
}

package storage

import (
	"context"
	"fmt"
	"github.com/vaporon4a/movie-helper/internal/clubreminder"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestClubRecoveryRetryAndIsolation(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "club.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, 10, 19, 16, 0, 0, 0, time.UTC)
	for _, chat := range []int64{-1, -2} {
		if err = store.EnsureChat(ctx, chat); err != nil {
			t.Fatal(err)
		}
		if err = store.SetZone(ctx, -chat, chat, "Europe/Moscow", now.AddDate(0, 0, -1)); err != nil {
			t.Fatal(err)
		}
	}
	if err = store.SetClubSchedule(ctx, 10, -1, 5, 2, "19:00", now.AddDate(0, 0, -1)); err != nil {
		t.Fatal(err)
	}
	settings, err := store.ClubSettings(ctx, -1, now)
	if err != nil {
		t.Fatal(err)
	}
	slots, _ := clubreminder.Slots(settings, now)
	for _, slot := range slots {
		if slot.At.Equal(now) {
			if err = store.ReserveClub(ctx, settings, slot); err != nil {
				t.Fatal(err)
			}
			if err = store.ReserveClub(ctx, settings, slot); err != nil {
				t.Fatal(err)
			}
		}
	}
	due, err := store.ClubDue(ctx, now)
	if err != nil || len(due) != 1 {
		t.Fatalf("due=%v err=%v", due, err)
	}
	d, claimed, err := store.ClaimClub(ctx, due[0].ID, now, clubreminder.Fallback(clubreminder.Explore, "friendly"))
	if err != nil || !claimed {
		t.Fatal(err, claimed)
	}
	if _, claimed, err = store.ClaimClub(ctx, d.ID, now, clubreminder.Fallback(clubreminder.Explore, "friendly")); err != nil || claimed {
		t.Fatal("duplicate claim", err)
	}
	if err = store.RecoverClub(ctx); err != nil {
		t.Fatal(err)
	}
	if err = store.ResolveClub(ctx, 11, -2, d.ID, "retry", now); err == nil {
		t.Fatal("cross-chat resolve accepted")
	}
	if err = store.ResolveClub(ctx, 12, -1, d.ID, "retry", now); err != nil {
		t.Fatal(err)
	}
	retry, claimed, err := store.ClaimClub(ctx, d.ID, now, clubreminder.Fallback(clubreminder.Explore, "friendly"))
	if err != nil || !claimed || retry.Text.Body != d.Text.Body || retry.Text.Tone != d.Text.Tone {
		t.Fatal("retry changed snapshot", err)
	}
	if err = store.FinishClub(ctx, retry, clubreminder.Sent, 123, time.Time{}, "", now); err != nil {
		t.Fatal(err)
	}
	if _, claimed, err = store.ClaimClub(ctx, d.ID, now, clubreminder.Fallback(clubreminder.Explore, "friendly")); err != nil || claimed {
		t.Fatal("sent reminder claimed", err)
	}
}
func TestClubPauseTimezoneStockAndAIWorkMigration(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "stock.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now()
	if err = s.EnsureChat(ctx, -1); err != nil {
		t.Fatal(err)
	}
	if err = s.SetZone(ctx, 1, -1, "Europe/Moscow", now); err != nil {
		t.Fatal(err)
	}
	if err = s.SetClubSchedule(ctx, 2, -1, 5, 2, "19:00", now); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		text := clubreminder.Text{Body: strings.Repeat(fmt.Sprintf("Уникальный%d вариант%d обсуждение%d ", i, i, i), 21), Intent: clubreminder.Explore, Tone: clubreminder.Tones[i%5], Provider: "test"}
		saved, saveErr := s.SaveClubText(ctx, -1, text, now)
		if saveErr != nil || saved != (i < 5) {
			t.Fatalf("item %d saved=%v err=%v", i, saved, saveErr)
		}
	}
	texts, err := s.ClubTexts(ctx, -1, now)
	if err != nil || len(texts) != 5 {
		t.Fatalf("stock=%d err=%v", len(texts), err)
	}
	duplicate, err := s.SaveClubText(ctx, -1, texts[0], now)
	if err != nil || duplicate {
		t.Fatal("duplicate accepted", err)
	}
	if err = s.SeedAIWork(ctx, now); err != nil {
		t.Fatal(err)
	}
	if err = s.PauseClub(ctx, 3, -1, now); err != nil {
		t.Fatal(err)
	}
	settings, err := s.ClubSettings(ctx, -1, now)
	if err != nil || settings.Enabled {
		t.Fatal(err)
	}
	if err = s.SetClubSchedule(ctx, 4, -1, 5, 2, "19:00", now); err != nil {
		t.Fatal(err)
	}
	revision := settings.Revision
	if err = s.SetZone(ctx, 5, -1, "Asia/Novosibirsk", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	settings, err = s.ClubSettings(ctx, -1, now)
	if err != nil || settings.Revision <= revision || settings.Zone != "Asia/Novosibirsk" {
		t.Fatal(settings, err)
	}
}

func TestClubMigrationPreservesExistingAIWork(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	p, err := s.migrator()
	must(t, err)
	_, err = p.DownTo(ctx, 14)
	must(t, err)
	_, err = s.db.ExecContext(t.Context(), `INSERT INTO ai_work(kind,scope_id,work_key,state,priority,next_attempt,attempts,last_error_code,created_at,updated_at) VALUES('fact_refill',-1,'migration-fact','waiting',100,123456,3,'provider_backoff',10,20)`)
	must(t, err)
	_, err = p.Up(ctx)
	must(t, err)
	var state, reason string
	var attempt, next int
	must(t, s.db.QueryRowContext(t.Context(), `SELECT state,attempts,next_attempt,last_error_code FROM ai_work WHERE work_key='migration-fact'`).Scan(&state, &attempt, &next, &reason))
	if state != "waiting" || attempt != 3 || next != 123456 || reason != "provider_backoff" {
		t.Fatal("migration changed AI work", state, attempt, next, reason)
	}
}

func TestClubScheduleChangeCannotRepeatLiveCycle(t *testing.T) {
	for _, change := range []string{"clock", "timezone", "pause_resume"} {
		for _, state := range []string{"sent", "unknown", "sending"} {
			t.Run(change+"/"+state, func(t *testing.T) {
				ctx := context.Background()
				s := testStore(t)
				now := time.Date(2026, 10, 19, 16, 0, 0, 0, time.UTC)
				must(t, s.EnsureChat(ctx, -1))
				must(t, s.SetZone(ctx, 1, -1, "Europe/Moscow", now.Add(-time.Hour)))
				must(t, s.SetClubSchedule(ctx, 2, -1, 5, 2, "19:00", now.Add(-time.Hour)))
				settings, err := s.ClubSettings(ctx, -1, now)
				must(t, err)
				slot := clubreminder.Slot{At: now, Cycle: "2026-10-09", Intent: clubreminder.Explore}
				must(t, s.ReserveClub(ctx, settings, slot))
				_, err = s.db.ExecContext(t.Context(), "UPDATE club_reminder_deliveries SET state=?", state)
				must(t, err)
				switch change {
				case "clock":
					must(t, s.SetClubSchedule(ctx, 3, -1, 5, 2, "21:00", now.Add(time.Minute)))
				case "timezone":
					must(t, s.SetZone(ctx, 3, -1, "Europe/Kaliningrad", now.Add(time.Minute)))
				case "pause_resume":
					must(t, s.PauseClub(ctx, 3, -1, now.Add(time.Minute)))
					must(t, s.SetClubSchedule(ctx, 4, -1, 5, 2, "21:00", now.Add(2*time.Minute)))
				}
				settings, err = s.ClubSettings(ctx, -1, now)
				must(t, err)
				slot.At = now.Add(2 * time.Hour)
				must(t, s.ReserveClub(ctx, settings, slot))
				var count int
				must(t, s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM club_reminder_deliveries").Scan(&count))
				if count != 1 {
					t.Fatalf("same logical reminder repeated after %s: count=%d", state, count)
				}
			})
		}
	}

}

func testClubText(index int) clubreminder.Text {
	return clubreminder.Text{Body: strings.Repeat(fmt.Sprintf("Уникальный%d вариант%d обсуждение%d ", index, index, index), 21), Intent: clubreminder.Explore, Tone: "friendly", Provider: "test"}
}
func TestClubTTLAndReturnedReservationUseInjectedTime(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	now := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
	must(t, s.EnsureChat(ctx, -1))
	must(t, s.SetZone(ctx, 1, -1, "Europe/Moscow", now.Add(-time.Hour)))
	must(t, s.SetClubSchedule(ctx, 2, -1, 5, 2, "19:00", now.Add(-time.Hour)))
	saved, err := s.SaveClubText(ctx, -1, testClubText(0), now.AddDate(0, 0, -clubreminder.StockDays-1))
	must(t, err)
	if !saved {
		t.Fatal("missing stale fixture")
	}
	for n := 1; n <= clubreminder.Target; n++ {
		saved, err = s.SaveClubText(ctx, -1, testClubText(n), now)
		must(t, err)
		if !saved {
			t.Fatal("missing fixture", n)
		}
	}
	settings, err := s.ClubSettings(ctx, -1, now)
	must(t, err)
	if settings.Stock != clubreminder.Target {
		t.Fatal("TTL used wall clock", settings.Stock)
	}
	slot := clubreminder.Slot{At: now, Cycle: "2029-12-14", Intent: clubreminder.Explore}
	must(t, s.ReserveClub(ctx, settings, slot))
	due, err := s.ClubDue(ctx, now)
	must(t, err)
	if len(due) != 1 {
		t.Fatal(due)
	}
	d, claimed, err := s.ClaimClub(ctx, due[0].ID, now, clubreminder.FallbackForUsage(clubreminder.Explore, 0))
	must(t, err)
	if !claimed || d.Text.ID == 0 {
		t.Fatal("AI text not reserved", d)
	}
	must(t, s.FinishClub(ctx, d, clubreminder.Retry, 0, now.Add(time.Minute), "telegram_rate_limit", now))
	saved, err = s.SaveClubText(ctx, -1, testClubText(10), now)
	must(t, err)
	if !saved {
		t.Fatal("stock not replenished after reservation")
	}
	must(t, s.PauseClub(ctx, 3, -1, now))
	texts, err := s.ClubTexts(ctx, -1, now)
	must(t, err)
	if len(texts) != clubreminder.Target {
		t.Fatalf("returned reservation expanded ready stock: %d", len(texts))
	}
	for _, text := range texts {
		if text.State != clubreminder.Ready {
			t.Fatal("reservation not returned", text)
		}
	}
}

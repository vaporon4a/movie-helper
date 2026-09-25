package storage

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/vaporon4a/movie-helper/internal/movieclub"
)

func TestMovieSchedulesAllowEveryWeekdayAndCancelOnlyPlannedRound(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	setup(t, store, -1)
	for weekday := range 7 {
		must(t, store.SetMovieSchedule(ctx, movieclub.Genre, int64(100+weekday), -1, weekday, "19:00", true, testNow))
	}
	schedules, err := store.MovieSchedules(ctx, -1)
	must(t, err)
	if len(schedules) != 7 {
		t.Fatalf("schedules = %#v", schedules)
	}

	options := movieclub.GenreOptions(1)
	wed := time.Date(2026, 9, 23, 19, 0, 0, 0, time.UTC)
	roundID, err := store.ReserveMovieRound(ctx, movieclub.Genre, -1, wed.Unix(), wed.Add(24*time.Hour).Unix(), options)
	must(t, err)
	// Editing the Wednesday slot invalidates the unpublished planned snapshot.
	must(t, store.SetMovieSchedule(ctx, movieclub.Genre, 200, -1, int(time.Wednesday), "20:00", true, testNow.Add(time.Minute)))
	round, err := store.MovieRound(ctx, -1, roundID)
	must(t, err)
	if round.State != movieclub.StateCancelled || round.ErrorCode != "schedule_changed" {
		t.Fatalf("round = %#v", round)
	}
}

func TestMovieSchedulesAreIndependentByFeature(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	setup(t, store, -1)
	must(t, store.SetMovieSchedule(ctx, movieclub.Genre, 501, -1, int(time.Saturday), "18:00", true, testNow))
	must(t, store.SetMovieSchedule(ctx, movieclub.Reference, 502, -1, int(time.Saturday), "20:00", true, testNow))
	schedules, err := store.MovieSchedules(ctx, -1)
	must(t, err)
	if len(schedules) != 2 || schedules[0].Feature != movieclub.Genre || schedules[1].Feature != movieclub.Reference {
		t.Fatalf("schedules=%#v", schedules)
	}
	must(t, store.PauseMovieSchedules(ctx, movieclub.Reference, 503, -1, testNow.Add(time.Minute)))
	schedules, err = store.MovieSchedules(ctx, -1)
	must(t, err)
	if !schedules[0].Enabled || schedules[1].Enabled {
		t.Fatalf("pause crossed feature boundary: %#v", schedules)
	}
}

func TestFailedTelegramSelectionCanBeRetriedAfterDeliveryFix(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	setup(t, store, -1)
	roundID, err := store.StartMovieRound(ctx, movieclub.Reference, 601, -1, testNow, 5*time.Minute, nil)
	must(t, err)
	_, err = store.db.ExecContext(ctx, "UPDATE movie_rounds SET state='failed',error_code='telegram_rejected' WHERE id=?", roundID)
	must(t, err)
	must(t, store.ResolveMovieRound(ctx, 602, -1, roundID, movieclub.ResolveRetry))
	round, err := store.MovieRound(ctx, -1, roundID)
	must(t, err)
	if round.State != movieclub.StateReady || round.ErrorCode != "" {
		t.Fatalf("round=%#v", round)
	}
}

func TestMovieRoundRejectsInvalidAndConcurrentTransitions(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	setup(t, store, -1)
	roundID, err := store.StartMovieRound(ctx, movieclub.Genre, 400, -1, testNow, 10*time.Minute, movieclub.GenreOptions(4))
	must(t, err)

	if _, err = store.ClaimMovieRound(ctx, roundID, movieclub.StatePlanned, movieclub.StatePublished, testNow); err == nil {
		t.Fatal("invalid transition planned -> published was accepted")
	}

	results := make(chan bool, 2)
	errorsOut := make(chan error, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Go(func() {
			claimed, claimErr := store.ClaimMovieRound(ctx, roundID, movieclub.StatePlanned, movieclub.StatePollCreating, testNow)
			results <- claimed
			errorsOut <- claimErr
		})
	}
	workers.Wait()
	close(results)
	close(errorsOut)

	claimed := 0
	for result := range results {
		if result {
			claimed++
		}
	}
	for claimErr := range errorsOut {
		if claimErr != nil {
			t.Fatal(claimErr)
		}
	}
	if claimed != 1 {
		t.Fatalf("concurrent claims = %d, want 1", claimed)
	}
}

func TestMovieRoundIsolationTransitionsAndRecovery(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	setup(t, store, -1)
	setup(t, store, -2)
	options := movieclub.GenreOptions(2)
	roundID, err := store.StartMovieRound(ctx, movieclub.Genre, 300, -1, testNow, 10*time.Minute, options)
	must(t, err)
	if _, err = store.MovieRound(ctx, -2, roundID); err == nil {
		t.Fatal("round crossed chat boundary")
	}
	if _, err = store.StartMovieRound(ctx, movieclub.Genre, 301, -1, testNow.Add(time.Minute), 10*time.Minute, options); !errors.Is(err, movieclub.ErrActiveRound) {
		t.Fatalf("second active round error = %v", err)
	}
	skippedID, err := store.ReserveMovieRound(ctx, movieclub.Genre, -1, testNow.Add(time.Hour).Unix(), testNow.Add(25*time.Hour).Unix(), options)
	must(t, err)
	if skippedID != 0 {
		t.Fatalf("scheduled overlap returned round %d", skippedID)
	}
	latest, err := store.LatestMovieRounds(ctx, -1)
	must(t, err)
	if latest[0].State != movieclub.StateCancelled || latest[0].ErrorCode != "active_round" {
		t.Fatalf("overlapping slot = %#v", latest[0])
	}
	claimed, err := store.ClaimMovieRound(ctx, roundID, movieclub.StatePlanned, movieclub.StatePollCreating, testNow)
	must(t, err)
	if !claimed {
		t.Fatal("planned round not claimed")
	}
	must(t, store.RecoverMovieRounds(ctx))
	round, err := store.MovieRound(ctx, -1, roundID)
	must(t, err)
	if round.State != movieclub.StateUnknown || round.ErrorCode != "restart_during_network" {
		t.Fatalf("recovered round = %#v", round)
	}
	if err = store.ResolveMovieRound(ctx, 302, -2, roundID, "cancel"); err == nil {
		t.Fatal("cross-chat resolution succeeded")
	}
	must(t, store.ResolveMovieRound(ctx, 303, -1, roundID, "cancel"))
}

package clubprofile_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/vaporon4a/movie-helper/internal/clubprofile"
	"github.com/vaporon4a/movie-helper/internal/storage"
)

type questions struct{ fail bool }

func (q *questions) InterestQuestion(_ context.Context, topic clubprofile.Topic, _ int64, _ uint64, _ time.Time) (clubprofile.Question, error) {
	if q.fail {
		return clubprofile.Question{}, errors.New("source unavailable")
	}
	return clubprofile.Question{Topic: topic, Intent: clubprofile.Interest, Text: "Какой вариант вам ближе?", Scale: "single-interest-v1", Version: clubprofile.QuestionV1, Anonymous: true, Revoting: true, Options: []clubprofile.Option{{Key: "a", Text: "Первый", Substantive: true}, {Key: "b", Text: "Второй", Substantive: true}}}, nil
}

type transport struct {
	now               *time.Time
	opens, closes     int
	openErr, closeErr error
	invalid           bool
}

func (t *transport) OpenProfilePoll(_ context.Context, p clubprofile.Poll) (string, int, error) {
	t.opens++
	return fmt.Sprintf("poll-%d", p.ID), int(p.ID), t.openErr
}
func (t *transport) CloseProfilePoll(_ context.Context, p clubprofile.Poll) (clubprofile.PollResult, error) {
	t.closes++
	total := 3
	if t.invalid {
		total = 4
	}
	return clubprofile.PollResult{PollID: p.TelegramID, Counts: []int{1, 2}, Total: &total, Closed: true, Source: clubprofile.SourceStop, ReceivedAt: *t.now}, t.closeErr
}

func profileApp(t *testing.T) (*storage.Store, *clubprofile.Service, *clubprofile.Coordinator, *transport, *questions, *time.Time) {
	t.Helper()
	ctx := t.Context()
	now := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err = store.EnsureChat(ctx, -1); err != nil {
		t.Fatal(err)
	}
	if err = store.SetZone(ctx, 100, -1, "UTC", now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	q := &questions{}
	tr := &transport{now: &now}
	service, err := clubprofile.NewService(store, q, map[int64]bool{-1: true}, slog.New(slog.DiscardHandler), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	c, err := clubprofile.NewCoordinator(service, tr)
	if err != nil {
		t.Fatal(err)
	}
	return store, service, c, tr, q, &now
}

func TestIndependentPollsAndClosedUpdateBeforeTick(t *testing.T) {
	store, s, c, tr, _, now := profileApp(t)
	ctx := t.Context()
	for i, topic := range []clubprofile.Topic{clubprofile.Genre, clubprofile.Reference} {
		if _, err := s.Start(ctx, topic, int64(i+1), -1, 5*time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if tr.opens != 2 {
		t.Fatal(tr.opens)
	}
	*now = now.Add(6 * time.Minute)
	if err := s.PollClosed(ctx, clubprofile.PollResult{PollID: "poll-1", Counts: []int{2, 0}, Total: new(2), Closed: true, Source: clubprofile.SourceUpdate, ReceivedAt: *now}); err != nil {
		t.Fatal(err)
	}
	if err := c.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	polls, err := store.ProfilePolls(ctx, -1, clubprofile.Completed)
	if err != nil {
		t.Fatal(err)
	}
	if len(polls) != 2 || tr.closes != 1 || *polls[0].Result.Total != 2 {
		t.Fatal(polls, tr)
	}
	if err = c.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if tr.opens != 2 || tr.closes != 1 {
		t.Fatal("duplicate IO")
	}
}

func TestUnknownSendNeverAutomaticallyRepeats(t *testing.T) {
	store, s, c, tr, _, now := profileApp(t)
	ctx := t.Context()
	tr.openErr = &clubprofile.DeliveryError{Kind: "unknown", Reason: "network"}
	if _, err := s.Start(ctx, clubprofile.Genre, 1, -1, 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := c.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	tr.openErr = nil
	*now = now.Add(time.Minute)
	if err := store.RecoverProfilePolls(ctx, *now); err != nil {
		t.Fatal(err)
	}
	if err := c.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if tr.opens != 1 {
		t.Fatal("ambiguous send repeated")
	}
}

func TestAlreadyClosedAndInvalidFinalDoNotInventVotes(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		t.Run(fmt.Sprint(invalid), func(t *testing.T) {
			store, s, c, tr, _, now := profileApp(t)
			ctx := t.Context()
			if _, err := s.Start(ctx, clubprofile.Genre, 1, -1, 5*time.Minute); err != nil {
				t.Fatal(err)
			}
			if err := c.Tick(ctx); err != nil {
				t.Fatal(err)
			}
			*now = now.Add(6 * time.Minute)
			tr.invalid = invalid
			if !invalid {
				tr.closeErr = &clubprofile.DeliveryError{Kind: "permanent", Reason: "poll_already_closed"}
			}
			if err := c.Tick(ctx); err != nil {
				t.Fatal(err)
			}
			polls, err := store.ProfilePolls(ctx, -1, clubprofile.Failed)
			if err != nil || len(polls) != 1 || polls[0].Result != nil {
				t.Fatal(polls, err)
			}
			if err = s.PollClosed(ctx, clubprofile.PollResult{PollID: "poll-1", Counts: []int{0, 0}, Total: new(0), Closed: true, Source: clubprofile.SourceUpdate, ReceivedAt: *now}); err != nil {
				t.Fatal(err)
			}
			polls, err = store.ProfilePolls(ctx, -1, clubprofile.Completed)
			if err != nil || len(polls) != 1 {
				t.Fatal(polls, err)
			}
		})
	}
}

func TestPreparationFailureDoesNotBlockFinalization(t *testing.T) {
	store, s, c, _, q, now := profileApp(t)
	ctx := t.Context()
	if _, err := s.Start(ctx, clubprofile.Genre, 1, -1, 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := c.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSchedule(ctx, 2, clubprofile.Schedule{ChatID: -1, Topic: clubprofile.Reference, Weekday: 3, Clock: "09:10", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(10 * time.Minute)
	q.fail = true
	if err := c.Tick(ctx); err == nil {
		t.Fatal("source error hidden")
	}
	polls, err := store.ProfilePolls(ctx, -1, clubprofile.Completed)
	if err != nil || len(polls) != 1 {
		t.Fatal(polls, err)
	}
}

func TestScheduleNoCatchupAndSuspendedChat(t *testing.T) {
	_, s, c, tr, _, now := profileApp(t)
	ctx := t.Context()
	if err := s.SetSchedule(ctx, 1, clubprofile.Schedule{ChatID: -1, Topic: clubprofile.Genre, Weekday: 3, Clock: "09:05", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(time.Hour)
	if err := c.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if tr.opens != 0 {
		t.Fatal("missed slot replayed")
	}
	if _, err := s.Start(ctx, clubprofile.Genre, 2, -2, 5*time.Minute); !errors.Is(err, clubprofile.ErrInvalid) {
		t.Fatal("foreign chat accepted", err)
	}
}

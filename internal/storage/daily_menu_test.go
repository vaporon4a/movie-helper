package storage

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/vaporon4a/movie-helper/internal/daily"
)

func menuIntent(t *testing.T, s *Store, token, action string) daily.MenuIntent {
	t.Helper()
	chat := int64(-1)
	c, e := s.DailyConfig(t.Context(), chat)
	must(t, e)
	i := daily.MenuIntent{Token: token, ChatID: chat, PanelID: 90, Kind: daily.Fact, Action: action, Revision: c.Revision, Expires: testNow.Add(10 * time.Minute)}
	must(t, s.CreateDailyIntent(t.Context(), i, testNow))
	return i
}
func TestDailyMenuChangesOnlySelectedFieldAndConsumesNonce(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	must(t, s.EnsureChat(ctx, -1))
	must(t, s.SetZone(ctx, 1, -1, "UTC", testNow))
	i := menuIntent(t, s, "clock", "time")
	i.Clock = "14:25"
	changed, err := s.ApplyDailyIntent(ctx, i, testNow)
	must(t, err)
	if !changed {
		t.Fatal("time not changed")
	}
	c, err := s.DailyConfig(ctx, -1)
	must(t, err)
	if c.Schedules[0].Clock != "14:25" || c.Schedules[0].Enabled {
		t.Fatal("time enabled disabled rubric", c)
	}
	if _, err = s.ApplyDailyIntent(ctx, i, testNow); !errors.Is(err, daily.ErrConflict) {
		t.Fatal("nonce replay", err)
	}
	i = menuIntent(t, s, "enable", "enable")
	_, err = s.ApplyDailyIntent(ctx, i, testNow)
	must(t, err)
	i = menuIntent(t, s, "pause", "pause")
	_, err = s.ApplyDailyIntent(ctx, i, testNow)
	must(t, err)
	c, err = s.DailyConfig(ctx, -1)
	must(t, err)
	if c.Schedules[0].Enabled || c.Schedules[0].Clock != "14:25" {
		t.Fatal(c)
	}
	i = menuIntent(t, s, "noop", "pause")
	before := c.Revision
	changed, err = s.ApplyDailyIntent(ctx, i, testNow)
	must(t, err)
	c, err = s.DailyConfig(ctx, -1)
	must(t, err)
	if changed || c.Revision != before {
		t.Fatal("noop changed revision")
	}
}
func TestDailyMenuStaleAfterEveryLegacyWriter(t *testing.T) {
	writers := []func(*Store) error{
		func(s *Store) error { return s.SetSchedule(t.Context(), 2, -1, daily.Meme, "08:00", true, testNow) },
		func(s *Store) error { return s.SetZone(t.Context(), 2, -1, "Europe/Moscow", testNow) },
		func(s *Store) error { return s.SetModeration(t.Context(), 2, -1, true, testNow) },
		func(s *Store) error { return s.Suspend(t.Context(), -1) },
		func(s *Store) error { return s.Resume(t.Context(), 2, -1) },
	}
	for index, writer := range writers {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			s := testStore(t)
			must(t, s.EnsureChat(t.Context(), -1))
			must(t, s.SetZone(t.Context(), 1, -1, "UTC", testNow))
			i := menuIntent(t, s, "stale", "enable")
			must(t, writer(s))
			if _, e := s.ApplyDailyIntent(t.Context(), i, testNow); !errors.Is(e, daily.ErrConflict) {
				t.Fatal(e)
			}
		})
	}
}
func TestDailyMenuPromptScopeExpiryAndClose(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	must(t, s.EnsureChat(ctx, -1))
	i := menuIntent(t, s, "input", "time")
	// Recreate with actor binding, which does not grant any privilege on the setting.
	_, err := s.db.ExecContext(ctx, "UPDATE interaction_intents SET actor_id=42 WHERE token=?", i.Token)
	must(t, err)
	must(t, s.BindDailyPrompt(ctx, i.Token, 91))
	if _, err = s.DailyReplyIntent(ctx, -1, 43, 91, testNow); !errors.Is(err, daily.ErrConflict) {
		t.Fatal("other actor")
	}
	if _, err = s.DailyReplyIntent(ctx, -2, 42, 91, testNow); !errors.Is(err, daily.ErrConflict) {
		t.Fatal("other chat")
	}
	if _, err = s.DailyReplyIntent(ctx, -1, 42, 92, testNow); !errors.Is(err, daily.ErrConflict) {
		t.Fatal("other prompt")
	}
	_, err = s.DailyReplyIntent(ctx, -1, 42, 91, testNow)
	must(t, err)
	if _, err = s.DailyReplyIntent(ctx, -1, 42, 91, testNow.Add(11*time.Minute)); !errors.Is(err, daily.ErrConflict) {
		t.Fatal("expired")
	}
	prompts, err := s.CancelDailyIntents(ctx, -1, 90, testNow)
	must(t, err)
	if len(prompts) != 1 || prompts[0] != 91 {
		t.Fatal(prompts)
	}
	if _, err = s.DailyReplyIntent(ctx, -1, 42, 91, testNow); !errors.Is(err, daily.ErrConflict) {
		t.Fatal("closed")
	}
}
func TestPreviewUnknownAndDifferentPanelsCannotDuplicate(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	must(t, s.EnsureChat(ctx, -1))
	i := menuIntent(t, s, "preview1", "preview")
	must(t, s.ClaimDailyPreview(ctx, i, testNow))
	other := menuIntent(t, s, "preview2", "preview")
	if err := s.ClaimDailyPreview(ctx, other, testNow.Add(2*time.Minute)); !errors.Is(err, daily.ErrConflict) {
		t.Fatal("lease expiry resent ambiguous request", err)
	}
	_, err := s.FinishDailyPreview(ctx, i, "unknown", 0, testNow)
	must(t, err)
	if err = s.ClaimDailyPreview(ctx, other, testNow.Add(3*time.Minute)); !errors.Is(err, daily.ErrConflict) {
		t.Fatal("unknown resent", err)
	}
}
func TestMenuMigrationPreservesConfigAcross24Upgrade(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	p, err := s.migrator()
	must(t, err)
	_, err = p.DownTo(ctx, 24)
	must(t, err)
	setup(t, s, -1)
	before, err := s.Schedules(ctx, -1)
	must(t, err)
	_, err = p.Up(ctx)
	must(t, err)
	after, err := s.Schedules(ctx, -1)
	must(t, err)
	if fmt.Sprint(before) != fmt.Sprint(after) {
		t.Fatal("upgrade altered schedules")
	}
	must(t, s.db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(new(string)))
}

func TestDailyPanelTombstoneRejectsAllActions(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	must(t, s.EnsureChat(ctx, -1))
	must(t, s.SetZone(ctx, 1, -1, "UTC", testNow))
	i := menuIntent(t, s, "close-enable", "enable")
	preview := menuIntent(t, s, "close-preview", "preview")
	// Simulate process failure between durable closing and prompt cleanup.
	must(t, s.CloseDailyPanel(ctx, -1, 90))
	if _, err := s.ApplyDailyIntent(ctx, i, testNow); !errors.Is(err, daily.ErrConflict) {
		t.Fatal("closed settings applied", err)
	}
	if err := s.ClaimDailyPreview(ctx, preview, testNow); !errors.Is(err, daily.ErrConflict) {
		t.Fatal("closed preview sent", err)
	}
}

func TestEnableNeedsZoneAndActiveChat(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	must(t, s.EnsureChat(ctx, -1))
	i := menuIntent(t, s, "no-zone", "enable")
	if _, err := s.ApplyDailyIntent(ctx, i, testNow); !errors.Is(err, daily.ErrConflict) {
		t.Fatal("enabled without zone", err)
	}
	must(t, s.SetZone(ctx, 1, -1, "UTC", testNow))
	must(t, s.Suspend(ctx, -1))
	i = menuIntent(t, s, "inactive", "enable")
	if _, err := s.ApplyDailyIntent(ctx, i, testNow); !errors.Is(err, daily.ErrConflict) {
		t.Fatal("enabled inactive chat", err)
	}
}

func TestDailyInputAndUnknownPreviewPersistAcrossRestart(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "restart.db")
	s, err := Open(ctx, path)
	must(t, err)
	must(t, s.EnsureChat(ctx, -1))
	must(t, s.SetZone(ctx, 1, -1, "UTC", testNow))
	i := menuIntent(t, s, "restart-input", "time")
	_, err = s.db.ExecContext(ctx, "UPDATE interaction_intents SET actor_id=42,prompt_id=91 WHERE token=?", i.Token)
	must(t, err)
	preview := menuIntent(t, s, "restart-preview", "preview")
	must(t, s.ClaimDailyPreview(ctx, preview, testNow))
	_, err = s.FinishDailyPreview(ctx, preview, "unknown", 0, testNow)
	must(t, err)
	must(t, s.Close())
	s, err = Open(ctx, path)
	must(t, err)
	defer s.Close()
	i, err = s.DailyReplyIntent(ctx, -1, 42, 91, testNow)
	must(t, err)
	i.Clock = "15:14"
	_, err = s.ApplyDailyIntent(ctx, i, testNow)
	must(t, err)
	other := menuIntent(t, s, "after-restart", "preview")
	if err = s.ClaimDailyPreview(ctx, other, testNow.Add(time.Minute)); !errors.Is(err, daily.ErrConflict) {
		t.Fatal("unknown replay after restart", err)
	}
}

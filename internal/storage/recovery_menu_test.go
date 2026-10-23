package storage

import (
	"errors"
	"testing"
	"time"

	"github.com/vaporon4a/movie-helper/internal/clubprofile"
	"github.com/vaporon4a/movie-helper/internal/recovery"
)

func TestRecoveryUnknownPollCannotRetryAndStaleConfirmCannotCancel(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	setup(t, s, -1)
	id := profilePoll(t, s, -1)
	_, err := s.db.ExecContext(ctx, "UPDATE club_profile_polls SET state='unknown' WHERE id=?", id)
	must(t, err)
	issues, err := s.RecoveryIssues(ctx, -1, testNow)
	must(t, err)
	if len(issues) != 1 || len(issues[0].Actions) != 1 || issues[0].Actions[0].Key != "cancel" {
		t.Fatal("unknown retry offered", issues)
	}
	_, err = s.PrepareRecovery(ctx, recovery.Confirmation{ChatID: -1, PanelID: 11, Issue: issues[0], Action: "retry"}, testNow)
	if !errors.Is(err, recovery.ErrConflict) {
		t.Fatal(err)
	}
	c, err := s.PrepareRecovery(ctx, recovery.Confirmation{ChatID: -1, PanelID: 11, Issue: issues[0], Action: "cancel"}, testNow)
	must(t, err)
	// ABA must invalidate confirmations even if the state string returns to unknown.
	_, err = s.db.ExecContext(ctx, "UPDATE club_profile_polls SET state='failed' WHERE id=?", id)
	must(t, err)
	_, err = s.db.ExecContext(ctx, "UPDATE club_profile_polls SET state='unknown' WHERE id=?", id)
	must(t, err)
	if err = s.ApplyRecovery(ctx, c.Token, -1, 11, testNow); !errors.Is(err, recovery.ErrConflict) {
		t.Fatal("ABA accepted", err)
	}
	c, err = s.PrepareRecovery(ctx, recovery.Confirmation{ChatID: -1, PanelID: 11, Issue: issues[0], Action: "cancel"}, testNow)
	must(t, err)
	if err = s.ApplyRecovery(ctx, c.Token, -2, 11, testNow); !errors.Is(err, recovery.ErrConflict) {
		t.Fatal("chat leak", err)
	}
	must(t, s.ApplyRecovery(ctx, c.Token, -1, 11, testNow))
	if err = s.ApplyRecovery(ctx, c.Token, -1, 11, testNow); !errors.Is(err, recovery.ErrConflict) {
		t.Fatal("replay", err)
	}
	polls, err := s.ProfilePolls(ctx, -1, clubprofile.Cancelled)
	must(t, err)
	if len(polls) != 1 {
		t.Fatal(polls)
	}
}
func TestExpiredReminderRecoveryNeverOffersRetry(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	setup(t, s, -1)
	_, err := s.db.ExecContext(ctx, "INSERT INTO club_reminder_deliveries(chat_id,revision,cycle_date,intent,slot_at,deadline,state) VALUES(-1,1,'2026-09-01','explore',?,?,'unknown')", testNow.Add(-7*time.Hour).Unix(), testNow.Add(-time.Hour).Unix())
	must(t, err)
	issues, err := s.RecoveryIssues(ctx, -1, testNow)
	must(t, err)
	if len(issues) != 1 {
		t.Fatal(issues)
	}
	for _, a := range issues[0].Actions {
		if a.Key == "retry" {
			t.Fatal("expired retry")
		}
	}
}

func TestUnknownMenuUnblocksOnlyAfterExplicitResolution(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	setup(t, s, -1)
	panel, err := s.AcquirePanel(ctx, -1, 42, "settings", testNow)
	must(t, err)
	must(t, s.FinishPanel(ctx, panel, 0, "unknown", testNow))
	issues, err := s.RecoveryIssues(ctx, -1, testNow)
	must(t, err)
	if len(issues) != 1 || issues[0].Domain != "ui" {
		t.Fatal(issues)
	}
	c, err := s.PrepareRecovery(ctx, recovery.Confirmation{ChatID: -1, PanelID: 41, Issue: issues[0], Action: "dismiss"}, testNow)
	must(t, err)
	must(t, s.ApplyRecovery(ctx, c.Token, -1, 41, testNow))
	var state string
	must(t, s.db.QueryRowContext(ctx, "SELECT state FROM interaction_surfaces WHERE token=?", panel.Token).Scan(&state))
	if state != "rejected" {
		t.Fatal("resolution retried", state)
	}
	next, err := s.AcquirePanel(ctx, -1, 42, "settings", testNow)
	must(t, err)
	if next.State != "claimed" || next.Token == panel.Token {
		t.Fatal(next)
	}
}

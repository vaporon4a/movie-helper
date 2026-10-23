package storage

import (
	"errors"
	"testing"
	"time"

	"github.com/vaporon4a/movie-helper/internal/daily"
)

func TestUIExpiryQueuesPromptWithoutAIAndPreservesUnknown(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	must(t, s.EnsureChat(ctx, -1))
	input := menuIntent(t, s, "expiry", "time")
	must(t, s.BindDailyPrompt(ctx, input.Token, 45))
	preview := menuIntent(t, s, "uncertain", "preview")
	must(t, s.ClaimDailyPreview(ctx, preview, testNow))
	must(t, s.ExpireInteractions(ctx, testNow.Add(time.Hour)))
	job, found, err := s.ClaimUICleanup(ctx, testNow.Add(time.Hour))
	must(t, err)
	if !found || job.MessageID != 45 || job.ChatID != -1 {
		t.Fatal(job, found)
	}
	issue, found, err := s.PreviewIssue(ctx, -1, daily.Fact, testNow.Add(time.Hour))
	must(t, err)
	if !found || issue.Token != preview.Token {
		t.Fatal("receipt lost", issue)
	}
	must(t, s.ResolvePreview(ctx, -1, issue, false, testNow.Add(time.Hour)))
	if err = s.ResolvePreview(ctx, -1, issue, false, testNow.Add(time.Hour)); !errors.Is(err, daily.ErrConflict) {
		t.Fatal("resolution replay", err)
	}
	if err = s.ClaimDailyPreview(ctx, preview, testNow.Add(time.Hour)); !errors.Is(err, daily.ErrConflict) {
		t.Fatal("old nonce revived", err)
	}
	next := preview
	next.Token = "fresh"
	next.Expires = testNow.Add(2 * time.Hour)
	must(t, s.CreateDailyIntent(ctx, next, testNow.Add(time.Hour)))
	must(t, s.ClaimDailyPreview(ctx, next, testNow.Add(time.Hour)))
}
func TestClosedStoreIsNotAStaleMenu(t *testing.T) {
	s := testStore(t)
	must(t, s.EnsureChat(t.Context(), -1))
	must(t, s.Close())
	_, err := s.DailyIntent(t.Context(), "token", -1, 4, testNow)
	if err == nil || errors.Is(err, daily.ErrConflict) {
		t.Fatal("storage error masked", err)
	}
}

func TestPromptReceiptAfterConcurrentCloseIsNotLost(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	must(t, s.EnsureChat(ctx, -1))
	i := menuIntent(t, s, "late-prompt", "time")
	must(t, s.CloseDailyPanel(ctx, -1, i.PanelID))
	must(t, s.BindDailyPrompt(ctx, i.Token, 456))
	var queued bool
	must(t, s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM interaction_cleanup WHERE chat_id=-1 AND message_id=456)").Scan(&queued))
	if !queued {
		t.Fatal("late prompt address lost")
	}
}

func TestGenericPrivatePanelDoesNotRequireDailyChat(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	p, err := s.AcquirePanel(ctx, 42, 42, "settings", testNow)
	must(t, err)
	must(t, s.FinishPanel(ctx, p, 41, "confirmed", testNow))
	must(t, s.CloseDailyPanel(ctx, 42, 41))
	closed, err := s.DailyPanelClosed(ctx, 42, 41)
	must(t, err)
	if !closed {
		t.Fatal("private menu not closed")
	}
}

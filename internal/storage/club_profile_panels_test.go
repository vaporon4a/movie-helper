package storage

import (
	"testing"

	"github.com/vaporon4a/movie-helper/internal/clubprofile"
)

func TestPreparationProjectionAndCardInvalidation(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	must(t, s.ConfigureFilmResearch(ctx, "on", map[int64]bool{-1: true}))
	b := approvedBundle(t, s)
	var draftID int64
	must(t, s.db.QueryRowContext(ctx, "SELECT id FROM club_watch_drafts WHERE watch_id=?", b.Preparation.WatchID).Scan(&draftID))
	_, err := s.db.ExecContext(ctx, "UPDATE club_watch_drafts SET message_id=777,rendered=version,card_page='details',card_page_id=watch_id WHERE id=?", draftID)
	must(t, err)
	prepareResearch(t, s, b)
	states, err := s.ProfilePreparationStates(ctx, -1)
	must(t, err)
	if len(states) != 1 || states[0].Stage != "ready" || !states[0].Approved {
		t.Fatal(states)
	}
	others, err := s.ProfilePreparationStates(ctx, -2)
	must(t, err)
	if len(others) != 0 {
		t.Fatal("chat leak")
	}
	cards, err := s.WatchCards(ctx, -1, testNow)
	must(t, err)
	if len(cards) == 0 {
		t.Fatal("ready did not invalidate panel")
	}
	page, id, closed, err := s.ProfilePage(ctx, -1, 777)
	must(t, err)
	if page != "details" || id != b.Preparation.WatchID || closed {
		t.Fatal(page, id, closed)
	}
	must(t, s.CloseWatchCard(ctx, -1, 777))
	must(t, s.SaveProfilePage(ctx, -1, 777, "watch", id))
	page, _, closed, err = s.ProfilePage(ctx, -1, 777)
	must(t, err)
	if page != "details" || !closed {
		t.Fatal("closed panel revived")
	}
	sc, q := researchSchedule(t, s)
	must(t, s.ReserveProfileSlot(ctx, sc, q, testNow, testNow))
	states, err = s.ProfilePreparationStates(ctx, -1)
	must(t, err)
	if states[0].Stage != "frozen" {
		t.Fatal(states)
	}
	sessions, err := s.ProfileSessions(ctx, -1)
	must(t, err)
	if sessions[0].Plan.Version != clubprofile.CoverageV2 {
		t.Fatal(sessions)
	}
	cards, err = s.WatchCards(ctx, -1, testNow)
	must(t, err)
	if len(cards) != 0 {
		t.Fatal("closed panel scheduled")
	}
}

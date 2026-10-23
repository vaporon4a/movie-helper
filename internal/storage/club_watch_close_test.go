package storage

import (
	"testing"
)

func TestClosedWatchCardStaysHiddenAndDoesNotAffectAnotherChat(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	for _, chat := range []int64{-1, -2} {
		must(t, s.EnsureChat(ctx, chat))
		d := addProfileWatch(t, s, chat, -chat, testNow.Format("2006-01-02"), testNow)
		claimed, err := s.ClaimWatchMessage(ctx, chat, d.ID, testNow)
		must(t, err)
		if !claimed {
			t.Fatal("claim")
		}
		must(t, s.BindWatchMessage(ctx, chat, d.ID, 99))
	}
	must(t, s.CloseWatchCard(ctx, -1, 99))
	// New evidence and future changes must not cause the closed UI to reappear.
	_, err := s.db.ExecContext(ctx, "UPDATE club_watch_drafts SET version=version+5")
	must(t, err)
	cards, err := s.WatchCards(ctx, -1, testNow)
	must(t, err)
	if len(cards) != 0 {
		t.Fatal("closed card still due", cards)
	}
	cards, err = s.WatchCards(ctx, -2, testNow)
	must(t, err)
	if len(cards) != 1 || cards[0].Closed {
		t.Fatal("other chat's card was closed", cards)
	}
}

func TestWatchCardCloseUpgradePreservesViewsAndAddsFooterRefresh(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	must(t, s.EnsureChat(ctx, -1))
	d := addProfileWatch(t, s, -1, 90, testNow.Format("2006-01-02"), testNow)
	_, err := s.db.ExecContext(ctx, "UPDATE club_watch_drafts SET message_id=99,rendered=version WHERE id=?", d.ID)
	must(t, err)
	m, err := s.migrator()
	must(t, err)
	_, err = m.DownTo(ctx, 23)
	must(t, err)
	_, err = m.Up(ctx)
	must(t, err)
	cards, err := s.WatchCards(ctx, -1, testNow)
	must(t, err)
	if len(cards) != 1 || cards[0].Closed || cards[0].Version <= cards[0].Rendered {
		t.Fatal("existing card was not queued for refresh", cards)
	}
	watches, err := s.ProfileWatches(ctx, -1)
	must(t, err)
	if len(watches) != 1 || watches[0].State != "active" || watches[0].Revision != 1 {
		t.Fatal("upgrade changed viewing", watches)
	}
}

package telegram

import (
	"errors"
	"strings"
	"testing"

	"github.com/vaporon4a/movie-helper/internal/clubprofile"
)

func TestWatchSectionsAndConsistentQueueWindow(t *testing.T) {
	v := clubProfileView{watches: []clubprofile.WatchEvent{{ID: 1, TMDBID: 603, State: "active", Film: clubprofile.Film{Title: "Матрица", Metadata: clubprofile.FilmMetadata{OriginalLanguage: "en"}}}}, sessions: []clubprofile.ReviewSession{{ID: 1, WatchID: 1, State: "researching"}, {ID: 2, WatchID: 2, State: "researching"}, {ID: 3, WatchID: 3, State: "queued"}}}
	text, k := v.watchCard(1)
	if strings.Contains(text, "Язык оригинала") || !strings.Contains(text, "TMDB") {
		t.Fatal("root not compact or credit absent", text)
	}
	if len(k.InlineKeyboard) != 3 {
		t.Fatal("too many root actions")
	}
	text, _ = v.watchSection(1, "details")
	if !strings.Contains(text, "Язык оригинала: en") {
		t.Fatal("missing details")
	}
	if reason := v.reviewWaitingReason(v.sessions[0]); reason != "" {
		t.Fatal("head waits on later researching session", reason)
	}
	if reason := v.reviewWaitingReason(v.sessions[2]); !strings.Contains(reason, "3 из 3") {
		t.Fatal(reason)
	}
}
func TestBackgroundWatchUpdateKeepsPageAndRespectsClosed(t *testing.T) {
	h, api := handler(t)
	s := appOf(t, h).store
	ctx := t.Context()
	var err error
	h.Profile, err = clubprofile.NewService(s, clubprofile.ProfileQuestions{History: s}, h.Allowed, h.Log, h.Now)
	if err != nil {
		t.Fatal(err)
	}
	h.Watches, err = clubprofile.NewWatchService(s, watchCatalog{}, h.Allowed, h.Log, h.Now)
	if err != nil {
		t.Fatal(err)
	}
	h.ProfilePanels = s
	u := update(500, -1, 42, "/movie_watched https://www.themoviedb.org/movie/603")
	h.Handle(ctx, nil, u)
	if err = h.showClubProfile(ctx, -1, 99, "details", 1); err != nil && !errors.Is(err, errResponseSent) {
		t.Fatal(err)
	}
	drafts, err := s.WatchCards(ctx, -1, h.Now())
	if err != nil || len(drafts) == 0 {
		t.Fatal(drafts, err)
	}
	if err = h.EditWatchCard(ctx, drafts[0]); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(api.edits[len(api.edits)-1].Text, "Покрыто") {
		t.Fatal("refresh reset details")
	}
	if err = s.CloseWatchCard(ctx, -1, 99); err != nil {
		t.Fatal(err)
	}
	before := len(api.edits)
	if err = h.EditWatchCard(ctx, drafts[0]); err != nil {
		t.Fatal(err)
	}
	if len(api.edits) != before {
		t.Fatal("stale worker revived closed panel")
	}
}

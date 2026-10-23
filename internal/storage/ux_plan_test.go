package storage

import (
	"errors"
	"testing"
	"time"

	"github.com/vaporon4a/movie-helper/internal/clubprofile"
	"github.com/vaporon4a/movie-helper/internal/clubreminder"
	"github.com/vaporon4a/movie-helper/internal/daily"
	"github.com/vaporon4a/movie-helper/internal/featurevote"
	"github.com/vaporon4a/movie-helper/internal/scheduleui"
)

func TestConfigPatchesAreIndependentAtomicAndInvalidateOldDialogs(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	setup(t, s, -1)
	c, err := s.ReadMoviesConfig(ctx, -1)
	must(t, err)
	changed, err := s.PatchMoviesConfig(ctx, -1, c.Revision, clubprofile.ConfigChange{Topic: clubprofile.Genre, Days: []clubprofile.SchedulePatch{{Weekday: 1, Clock: new("10:15")}}}, testNow)
	must(t, err)
	if !changed {
		t.Fatal("clock was not saved")
	}
	c, err = s.ReadMoviesConfig(ctx, -1)
	must(t, err)
	if len(c.Schedules) != 1 || c.Schedules[0].Enabled {
		t.Fatal("time enabled poll", c)
	}
	_, err = s.PatchMoviesConfig(ctx, -1, c.Revision, clubprofile.ConfigChange{Topic: clubprofile.Genre, Days: []clubprofile.SchedulePatch{{Weekday: 1, Clock: new("11:00")}, {Weekday: 8, Clock: new("13:00")}}}, testNow)
	if err == nil {
		t.Fatal("invalid batch accepted")
	}
	after, err := s.ReadMoviesConfig(ctx, -1)
	must(t, err)
	if after.Revision != c.Revision || after.Schedules[0].Clock != "10:15" {
		t.Fatal("partial batch", after)
	}
	ui := scheduleui.New(s, s)
	d, err := ui.Begin(ctx, -1, 101, "genre", testNow)
	must(t, err)
	d.Step = "confirm"
	d.Movies.Days[1].Enabled = true
	d, err = ui.Update(ctx, d, testNow)
	must(t, err)
	must(t, s.SetProfileSchedule(ctx, 93, clubprofile.Schedule{ChatID: -1, Topic: clubprofile.Reference, Weekday: 3, Clock: "12:00", Enabled: true, Effective: testNow}))
	if _, err = ui.Commit(ctx, d, testNow); !errors.Is(err, clubprofile.ErrConfigConflict) {
		t.Fatal("stale dialog saved", err)
	}
	club, err := s.ReadClubConfig(ctx, -1)
	must(t, err)
	_, err = s.PatchClubConfig(ctx, -1, club.Revision, clubreminder.ConfigChange{Clock: new("15:23")}, testNow)
	must(t, err)
	club, err = s.ReadClubConfig(ctx, -1)
	must(t, err)
	if club.Settings.Enabled || club.Settings.Clock != "15:23" {
		t.Fatal(club)
	}
	ideas, err := s.ReadIdeasConfig(ctx, -1)
	must(t, err)
	_, err = s.PatchIdeasConfig(ctx, -1, ideas.Revision, featurevote.ConfigChange{Clock: new("14:30")}, testNow)
	must(t, err)
	ideas, err = s.ReadIdeasConfig(ctx, -1)
	must(t, err)
	if ideas.Schedule.Enabled || ideas.Schedule.Clock != "14:30" {
		t.Fatal(ideas)
	}
}
func TestPanelRegistryUnknownDoesNotSendTwiceAndScopesRequesters(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	setup(t, s, -1)
	p, err := s.AcquirePanel(ctx, -1, 42, "settings", testNow)
	must(t, err)
	if p.State != "claimed" {
		t.Fatal(p)
	}
	other, err := s.AcquirePanel(ctx, -1, 42, "settings", testNow)
	must(t, err)
	if other.State == "claimed" || other.Token != p.Token {
		t.Fatal("double send", other)
	}
	must(t, s.FinishPanel(ctx, p, 0, "unknown", testNow))
	other, err = s.AcquirePanel(ctx, -1, 42, "settings", testNow.Add(48*time.Hour))
	must(t, err)
	if other.State != "unknown" {
		t.Fatal("TTL retried unknown", other)
	}
	separate, err := s.AcquirePanel(ctx, -1, 43, "settings", testNow)
	must(t, err)
	if separate.State != "claimed" {
		t.Fatal("another member locked", separate)
	}
	must(t, s.FinishPanel(ctx, separate, 190, "confirmed", testNow))
	must(t, s.CloseDailyPanel(ctx, -1, 190))
	reopen, err := s.AcquirePanel(ctx, -1, 43, "settings", testNow)
	must(t, err)
	if reopen.State != "claimed" {
		t.Fatal("closed UI reused", reopen)
	}
}
func provisionalWatch(t *testing.T, s *Store) clubprofile.WatchDraft {
	t.Helper()
	ctx := t.Context()
	setup(t, s, -1)
	input, err := clubprofile.ParseWatchInput("https://www.themoviedb.org/movie/603", "UTC", testNow)
	must(t, err)
	input.Provisional = true
	d, err := s.CreateWatchDraft(ctx, 882, -1, input, testNow)
	must(t, err)
	must(t, s.CreateDailyIntent(ctx, daily.MenuIntent{Token: "watch-input", ChatID: -1, PanelID: 151, Kind: "watch", Action: "time", Expires: testNow.Add(time.Hour)}, testNow))
	must(t, s.AttachWatchDialog(ctx, -1, d.ID, 151, "watch-input", testNow))
	d, err = s.WatchDraft(ctx, -1, d.ID)
	must(t, err)
	d, err = s.FinishWatchResolution(ctx, d, []clubprofile.Film{{TMDBID: 603, Title: "Матрица", Source: "tmdb", FetchedAt: testNow}}, "", testNow)
	must(t, err)
	return d
}
func TestWatchedWizardDoesNotSaveBeforeConfirmationAndRejectsReplay(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	d := provisionalWatch(t, s)
	watches, err := s.ProfileWatches(ctx, -1)
	must(t, err)
	if len(watches) != 0 || d.State != "choice" || d.Reason != "confirm_required" {
		t.Fatal("premature viewing", d, watches)
	}
	d, err = s.DateWatchDialog(ctx, -1, d.ID, d.Version, "2026-09-21", testNow)
	must(t, err)
	result, err := s.ConfirmWatchDialog(ctx, -1, d.ID, d.Version, "", testNow)
	must(t, err)
	if result.WatchID == 0 || result.State != "saved" {
		t.Fatal(result)
	}
	if _, err = s.ConfirmWatchDialog(ctx, -1, d.ID, d.Version, "", testNow); !errors.Is(err, clubprofile.ErrConflict) {
		t.Fatal("replayed confirmation", err)
	}
	watches, err = s.ProfileWatches(ctx, -1)
	must(t, err)
	if len(watches) != 1 || watches[0].Date != "2026-09-21" {
		t.Fatal(watches)
	}
}
func TestClosedProvisionalAndNavigationCannotSave(t *testing.T) {
	for _, mode := range []string{"close", "navigate"} {
		t.Run(mode, func(t *testing.T) {
			s := testStore(t)
			d := provisionalWatch(t, s)
			if mode == "close" {
				must(t, s.CloseDailyPanel(t.Context(), -1, 151))
			} else {
				must(t, s.SetPanelScreen(t.Context(), -1, 151, "menu"))
			}
			if _, err := s.ConfirmWatchDialog(t.Context(), -1, d.ID, d.Version, "", testNow); !errors.Is(err, clubprofile.ErrConflict) {
				t.Fatal(err)
			}
		})
	}
}
func TestSchema25CutoverPreservesAllClosedFlagCombinations(t *testing.T) {
	for _, flags := range [][2]bool{{true, false}, {false, true}, {true, true}} {
		t.Run(time.Now().Format("150405.000000000"), func(t *testing.T) {
			s := testStore(t)
			ctx := t.Context()
			must(t, s.EnsureChat(ctx, -1))
			d := addProfileWatch(t, s, -1, 90, testNow.Format(time.DateOnly), testNow)
			_, err := s.db.ExecContext(ctx, "UPDATE club_watch_drafts SET message_id=99 WHERE id=?", d.ID)
			must(t, err)
			m, err := s.migrator()
			must(t, err)
			_, err = m.DownTo(ctx, 25)
			must(t, err)
			_, err = s.db.ExecContext(ctx, "UPDATE club_watch_drafts SET card_closed=? WHERE id=?", flags[1], d.ID)
			must(t, err)
			_, err = s.db.ExecContext(ctx, "INSERT INTO daily_menu_panels(chat_id,panel_id,closed) VALUES(-1,99,?)", flags[0])
			must(t, err)
			_, err = s.db.ExecContext(ctx, "INSERT INTO daily_menu_intents(token,chat_id,panel_id,kind,action,revision,expires_at) VALUES('legacy',-1,100,'fact','preview',0,?)", testNow.Add(time.Hour).Unix())
			must(t, err)
			_, err = s.db.ExecContext(ctx, "INSERT INTO daily_menu_previews(chat_id,kind,token,state,until_at) VALUES(-1,'fact','unknown25','unknown',?)", testNow.Unix())
			must(t, err)
			_, err = s.db.ExecContext(ctx, "INSERT INTO daily_menu_intents(token,chat_id,panel_id,kind,action,revision,actor_id,prompt_id,expires_at) VALUES('input25',-1,102,'fact','time',0,42,103,?)", testNow.Add(time.Hour).Unix())
			must(t, err)
			_, err = m.Up(ctx)
			must(t, err)
			issue, found, err := s.PreviewIssue(ctx, -1, "fact", testNow)
			must(t, err)
			if !found || issue.Token != "unknown25" || issue.Revision != 1 {
				t.Fatal("unknown receipt lost", issue)
			}
			input, err := s.DailyReplyIntent(ctx, -1, 42, 103, testNow)
			must(t, err)
			if input.Token != "input25" {
				t.Fatal("input binding lost")
			}

			closed, err := s.DailyPanelClosed(ctx, -1, 99)
			must(t, err)
			if !closed {
				t.Fatal("closed flag lost")
			}
			_, _, closed, err = s.ProfilePage(ctx, -1, 99)
			must(t, err)
			if !closed {
				t.Fatal("watch reopened")
			}
			_, err = s.DailyIntent(ctx, "legacy", -1, 100, testNow)
			must(t, err)
		})
	}
}

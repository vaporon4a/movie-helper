package telegram

import (
	"context"
	"strings"
	"testing"

	"github.com/vaporon4a/movie-helper/internal/clubreminder"
)

func TestClubCommandsAuthorizationReplayAndPreview(t *testing.T) {
	h, a := handler(t)
	store := appOf(t, h).store
	h.Club = clubreminder.NewService(store, h.Now, h.Log)
	ctx := context.Background()
	h.Handle(ctx, nil, update(100, -1, 42, "/timezone Europe/Moscow"))
	h.Handle(ctx, nil, update(1, -1, 7, "/club_schedule fri 2 19:00"))
	settings, err := store.ClubSettings(ctx, -1, h.Now())
	if err != nil || settings.Enabled {
		t.Fatal("non-admin changed schedule", settings, err)
	}
	h.Handle(ctx, nil, update(2, -1, 42, "/club_schedule fri 2 19:00"))
	settings, err = store.ClubSettings(ctx, -1, h.Now())
	if err != nil || !settings.Enabled || settings.Weekday != 5 || settings.Ordinal != 2 {
		t.Fatal(settings, err)
	}
	revision := settings.Revision
	h.Handle(ctx, nil, update(2, -1, 42, "/club_schedule sat last 18:00"))
	settings, err = store.ClubSettings(ctx, -1, h.Now())
	if err != nil || settings.Revision != revision {
		t.Fatal("replay changed schedule", settings, err)
	}
	text := clubreminder.Fallback(clubreminder.Explore, "friendly")
	if saved, saveErr := store.SaveClubText(ctx, -1, text, h.Now()); saveErr != nil || !saved {
		t.Fatal(saved, saveErr)
	}
	h.Handle(ctx, nil, update(3, -1, 42, "/club_preview"))
	if !strings.Contains(a.messages[len(a.messages)-1].Text, text.Body) {
		t.Fatal("preview missing text")
	}
	texts, err := store.ClubTexts(ctx, -1, h.Now())
	if err != nil || len(texts) != 1 || texts[0].State != clubreminder.Ready {
		t.Fatal("preview consumed stock", texts, err)
	}
	h.Handle(ctx, nil, update(4, -1, 7, "/club_pause"))
	settings, err = store.ClubSettings(ctx, -1, h.Now())
	if err != nil || !settings.Enabled {
		t.Fatal("non-admin paused", err)
	}
	h.Handle(ctx, nil, update(5, -1, 42, "/club_pause"))
	settings, err = store.ClubSettings(ctx, -1, h.Now())
	if err != nil || settings.Enabled {
		t.Fatal("pause failed", err)
	}
}

package telegram

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"
	"github.com/vaporon4a/movie-helper/internal/clubprofile"
)

type statisticsViewStore struct {
	in     clubprofile.StatisticsInput
	sc     clubprofile.StatisticsSettings
	writes int
}

func (s *statisticsViewStore) StatisticsInput(_ context.Context, chat int64) (clubprofile.StatisticsInput, error) {
	return s.in, nil
}
func (s *statisticsViewStore) StatisticsSettings(_ context.Context, chat int64) (clubprofile.StatisticsSettings, error) {
	return s.sc, nil
}
func (s *statisticsViewStore) SetStatisticsSettings(_ context.Context, sc clubprofile.StatisticsSettings, at time.Time) error {
	s.writes++
	s.sc = sc
	s.sc.Revision++
	return nil
}
func TestStatisticsPagesPreviewAndEqualSettings(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	store := &statisticsViewStore{in: clubprofile.StatisticsInput{ChatID: -1, Zone: "Asia/Novosibirsk"}, sc: clubprofile.StatisticsSettings{ChatID: -1, Zone: "Asia/Novosibirsk", Clock: "09:00", Weekday: 5}}
	service := &clubprofile.StatisticsService{Reader: store, Settings: store, Allowed: map[int64]bool{-1: true}, Now: func() time.Time { return now }, Log: slog.New(slog.DiscardHandler)}
	h := &Handler{Statistics: service}
	for _, page := range []string{"stats", "stats_week", "stats_month", "stats_films", "stats_rules", "stats_preview", "stats_settings"} {
		text, keys, err := h.statisticsPage(t.Context(), -1, page, 0)
		if err != nil || text == "" || keys == nil || len([]rune(text)) > 4000 {
			t.Fatalf("%s: %s %v", page, text, err)
		}
		keys = closableKeyboard(keys)
		last := keys.InlineKeyboard[len(keys.InlineKeyboard)-1]
		if len(last) != 1 || !strings.Contains(last[0].Text, "Закрыть") {
			t.Fatalf("missing close %s: %+v", page, keys)
		}
	}
	if store.writes != 0 {
		t.Fatal("preview mutated settings")
	}
	// Application deliberately has no administrator or author argument.
	if err := service.Change(t.Context(), -1, 0, "toggle"); err != nil {
		t.Fatal(err)
	}
	if !store.sc.Enabled || store.writes != 1 {
		t.Fatal("member cannot enable")
	}
	if err := service.Change(t.Context(), -1, 0, "toggle"); err == nil {
		t.Fatal("stale revision replayed")
	}
	if _, err := service.Snapshot(t.Context(), -2, "all"); err == nil {
		t.Fatal("cross-chat access")
	}
}

func TestStatisticsRealCallbackWithRegisteredPanels(t *testing.T) {
	h, api := handler(t)
	attachMenuServices(t, h)
	s := appOf(t, h).store
	var err error
	h.Watches, err = clubprofile.NewWatchService(s, watchCatalog{}, h.Allowed, h.Log, h.Now)
	if err != nil {
		t.Fatal(err)
	}
	h.ProfilePanels = s
	h.Statistics = &clubprofile.StatisticsService{Reader: s, Reports: s, Settings: s, Allowed: h.Allowed, Now: h.Now, Log: h.Log}
	h.Handle(t.Context(), nil, update(1900, -1, 42, "/timezone Asia/Novosibirsk"))
	u := menuClick(1901, -1, 42, "home", models.ChatTypeSupergroup)
	u.CallbackQuery.Data = "cp:view:stats_settings:0"
	h.Handle(t.Context(), nil, u)
	if len(api.edits) == 0 || !strings.Contains(api.edits[len(api.edits)-1].Text, "Недельная сводка") {
		t.Fatal("settings not rendered")
	}
	u.ID = 1902
	u.CallbackQuery.From.ID = 43
	u.CallbackQuery.Data = "cp:stat:toggle:0"
	h.Handle(t.Context(), nil, u)
	sc, err := s.StatisticsSettings(t.Context(), -1)
	if err != nil || !sc.Enabled {
		t.Fatal("member callback blocked", sc, err)
	}
	u.ID = 1903
	h.Handle(t.Context(), nil, u)
	after, err := s.StatisticsSettings(t.Context(), -1)
	if err != nil || !after.Enabled || after.Revision != 1 {
		t.Fatal("stale callback mutated", after, err)
	}
}

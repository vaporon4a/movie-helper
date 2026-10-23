package telegram

import (
	"testing"
	"time"

	"github.com/go-telegram/bot"
	"github.com/vaporon4a/movie-helper/internal/daily"
	"github.com/vaporon4a/movie-helper/internal/interaction"
)

func TestUIMaintenanceDeletesOnlyPersistedPromptAndRetriesFailure(t *testing.T) {
	h, api := handler(t)
	attachDailyMenu(t, h)
	s := appOf(t, h).store
	i, err := h.Interactions.NewIntent(t.Context(), daily.MenuIntent{ChatID: -1, PanelID: 700, Kind: daily.Fact, Action: "time", ActorID: 42}, h.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.BindDailyPrompt(t.Context(), i.Token, 701); err != nil {
		t.Fatal(err)
	}
	worker := interaction.Maintenance{Store: s, Remover: h, Log: h.Log}
	api.deleteErr = bot.ErrorForbidden
	if err = worker.Tick(t.Context(), h.Now().Add(11*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if len(api.deletes) != 1 || api.deletes[0].MessageID != 701 {
		t.Fatal("deleted arbitrary message", api.deletes)
	}
	api.deleteErr = nil
	if err = worker.Tick(t.Context(), h.Now().Add(20*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if len(api.deletes) != 2 {
		t.Fatal("cleanup lost", api.deletes)
	}
	if err = worker.Tick(t.Context(), h.Now().Add(30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if len(api.deletes) != 2 {
		t.Fatal("settled cleanup retried")
	}
}

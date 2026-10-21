package telegram

import (
	"context"
	"strings"
	"testing"

	"github.com/vaporon4a/movie-helper/internal/featurevote"
)

func TestMemberManagesSharedIdeaWithinChat(t *testing.T) {
	h, _ := handler(t)
	attachFeatureService(t, h)
	ctx := context.Background()
	h.Handle(ctx, nil, update(900, -1, 7, "/idea Добавить общий журнал просмотренных фильмов"))
	h.Handle(ctx, nil, update(901, -2, 8, "/idea_remove 1"))
	ideas, err := h.Features.Mine(ctx, -1, 7)
	if err != nil || len(ideas) != 1 {
		t.Fatal("another chat changed idea", ideas, err)
	}
	for i, command := range []string{"/idea_remove 1", "/idea_restore 1", "/idea_done 1", "/idea_restore 1"} {
		h.Handle(ctx, nil, update(int64(902+i), -1, 8, command))
		ideas, err = h.Features.Mine(ctx, -1, 7)
		wantActive := strings.HasPrefix(command, "/idea_restore")
		if err != nil || (len(ideas) == 1) != wantActive {
			t.Fatalf("command=%s ideas=%v err=%v", command, ideas, err)
		}
	}
}

func TestMemberCannotEditIdeaInOpenRound(t *testing.T) {
	h, _ := handler(t)
	store := attachFeatureService(t, h)
	ctx := context.Background()
	h.Handle(ctx, nil, update(910, -1, 7, "/idea Добавить общий журнал просмотренных фильмов"))
	h.Handle(ctx, nil, update(911, -1, 8, "/idea Делиться отзывами после каждой встречи киноклуба"))
	h.Handle(ctx, nil, update(912, -1, 8, "/idea_round 10m"))
	settings, err := store.FeatureSettings(ctx, -1)
	if err != nil || len(settings.Latest) == 0 {
		t.Fatal("member could not start voting", settings, err)
	}
	if err = store.SaveFeatureRoundOptions(ctx, settings.Latest[0].ID, []featurevote.Option{{IdeaID: 1, Title: "Журнал просмотров"}, {IdeaID: 2, Title: "Отзывы после встречи"}}); err != nil {
		t.Fatal(err)
	}
	h.Handle(ctx, nil, update(913, -1, 9, "/idea_remove 1"))
	ideas, err := h.Features.Mine(ctx, -1, 7)
	if err != nil || len(ideas) != 1 {
		t.Fatal("editing invalidated active ballot", ideas, err)
	}
}

func TestManagementHelpForMembers(t *testing.T) {
	h, api := handler(t)
	for i, command := range []string{"/help_manage", "/help_admin"} {
		h.Handle(context.Background(), nil, update(int64(920+i), -1, 7, command))
		if got := api.messages[len(api.messages)-1].Text; got != helpManage || !strings.Contains(got, "всем участникам клуба") {
			t.Fatalf("help=%q", got)
		}
	}
}

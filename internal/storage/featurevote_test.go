package storage

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vaporon4a/movie-helper/internal/featurevote"
)

func addFeature(t *testing.T, store *Store, op, author int64, text, hash string) featurevote.Idea {
	t.Helper()
	idea, err := store.AddFeature(context.Background(), op, -1, author, text, hash, testNow)
	must(t, err)
	return idea
}

func openFeatureRound(t *testing.T, store *Store, token string, ideas []featurevote.Idea) int64 {
	t.Helper()
	ctx := context.Background()
	id, err := store.StartFeatureRound(ctx, 100, -1, testNow, time.Hour, token)
	must(t, err)
	options := make([]featurevote.Option, len(ideas))
	for i, idea := range ideas {
		options[i] = featurevote.Option{IdeaID: idea.ID, Title: "Идея", Text: idea.Text}
	}
	must(t, store.SaveFeatureRoundOptions(ctx, id, options))
	claimed, err := store.ClaimFeatureRound(ctx, id, featurevote.RoundPlanned, featurevote.RoundOpening, testNow)
	must(t, err)
	if !claimed {
		t.Fatal("round not claimed")
	}
	must(t, store.OpenFeatureRound(ctx, id, 99, testNow))
	return id
}

func TestFeatureIdeasRateLimitDedupAndOwnership(t *testing.T) {
	store := testStore(t)
	setup(t, store, -1)
	first := addFeature(t, store, 1, 7, "Первая достаточно длинная идея для нашего бота", "one")
	addFeature(t, store, 2, 7, "Вторая достаточно длинная идея для нашего бота", "two")
	if _, err := store.AddFeature(context.Background(), 3, -1, 7, "Третья достаточно длинная идея для нашего бота", "three", testNow); !errors.Is(err, featurevote.ErrRateLimit) {
		t.Fatalf("rate limit error=%v", err)
	}
	if _, err := store.AddFeature(context.Background(), 4, -1, 8, "Повтор первой идеи с тем же хэшем", "one", testNow); !errors.Is(err, featurevote.ErrDuplicate) {
		t.Fatalf("duplicate error=%v", err)
	}
	if err := store.ChangeFeatureState(context.Background(), 5, -1, first.ID, 8, featurevote.IdeaRemoved, false, testNow); !errors.Is(err, featurevote.ErrConflict) {
		t.Fatalf("foreign author removed idea: %v", err)
	}
	must(t, store.ChangeFeatureState(context.Background(), 6, -1, first.ID, 7, featurevote.IdeaRemoved, false, testNow))
}

func TestFeatureVoteWinnerMovesOnlyWinnerToBacklog(t *testing.T) {
	store := testStore(t)
	setup(t, store, -1)
	first := addFeature(t, store, 10, 7, "Добавить общий список просмотренных фильмов", "first")
	second := addFeature(t, store, 11, 8, "Добавить напоминания перед началом киновечера", "second")
	roundID := openFeatureRound(t, store, "winner-token", []featurevote.Idea{first, second})
	must(t, store.VoteFeature(context.Background(), "winner-token", 101, first.ID, testNow.Add(time.Minute)))
	must(t, store.VoteFeature(context.Background(), "winner-token", 102, first.ID, testNow.Add(time.Minute)))
	must(t, store.VoteFeature(context.Background(), "winner-token", 103, second.ID, testNow.Add(time.Minute)))
	claimed, err := store.ClaimFeatureRound(context.Background(), roundID, featurevote.RoundOpen, featurevote.RoundClosing, testNow.Add(time.Hour))
	must(t, err)
	if !claimed {
		t.Fatal("open round not claimed")
	}
	result, err := store.FinalizeFeatureRound(context.Background(), roundID, "unused-runoff", testNow.Add(time.Hour))
	must(t, err)
	if result.Outcome != "winner" || result.WinnerID != first.ID {
		t.Fatalf("result=%#v", result)
	}
	backlog, err := store.FeatureBacklog(context.Background(), -1)
	must(t, err)
	if len(backlog) != 1 || backlog[0].ID != first.ID {
		t.Fatalf("backlog=%#v", backlog)
	}
	active, err := store.ActiveFeatures(context.Background(), -1)
	must(t, err)
	if len(active) != 1 || active[0].ID != second.ID {
		t.Fatalf("active=%#v", active)
	}
	view, err := store.FeatureView(context.Background(), "winner-token", 101)
	must(t, err)
	if view.SelectedID != 0 {
		t.Fatal("personal votes were retained after tally")
	}
}

func TestFeatureVoteTieCreatesRunoffWithLeaders(t *testing.T) {
	store := testStore(t)
	setup(t, store, -1)
	first := addFeature(t, store, 20, 7, "Добавить общий список просмотренных фильмов", "tie-first")
	second := addFeature(t, store, 21, 8, "Добавить напоминания перед началом киновечера", "tie-second")
	roundID := openFeatureRound(t, store, "tie-token", []featurevote.Idea{first, second})
	must(t, store.VoteFeature(context.Background(), "tie-token", 101, first.ID, testNow.Add(time.Minute)))
	must(t, store.VoteFeature(context.Background(), "tie-token", 102, second.ID, testNow.Add(time.Minute)))
	claimed, err := store.ClaimFeatureRound(context.Background(), roundID, featurevote.RoundOpen, featurevote.RoundClosing, testNow.Add(time.Hour))
	must(t, err)
	if !claimed {
		t.Fatal("open round not claimed")
	}
	result, err := store.FinalizeFeatureRound(context.Background(), roundID, "runoff-token", testNow.Add(time.Hour))
	must(t, err)
	if result.Outcome != "tie" || result.RunoffID == 0 {
		t.Fatalf("result=%#v", result)
	}
	runoff, err := store.FeatureRound(context.Background(), result.RunoffID)
	must(t, err)
	options, err := store.FeatureRoundOptions(context.Background(), runoff.ID)
	must(t, err)
	if runoff.ParentID != roundID || runoff.State != featurevote.RoundPlanned || len(options) != 2 {
		t.Fatalf("runoff=%#v options=%#v", runoff, options)
	}
	claimed, err = store.ClaimFeatureRound(context.Background(), runoff.ID, featurevote.RoundPlanned, featurevote.RoundOpening, testNow.Add(time.Hour))
	must(t, err)
	if !claimed {
		t.Fatal("runoff not claimed")
	}
	must(t, store.OpenFeatureRound(context.Background(), runoff.ID, 100, testNow.Add(time.Hour)))
	must(t, store.VoteFeature(context.Background(), "runoff-token", 201, first.ID, testNow.Add(time.Hour+time.Minute)))
	must(t, store.VoteFeature(context.Background(), "runoff-token", 202, second.ID, testNow.Add(time.Hour+time.Minute)))
	claimed, err = store.ClaimFeatureRound(context.Background(), runoff.ID, featurevote.RoundOpen, featurevote.RoundClosing, testNow.Add(featurevote.RunoffDuration+time.Hour))
	must(t, err)
	if !claimed {
		t.Fatal("open runoff not claimed")
	}
	final, err := store.FinalizeFeatureRound(context.Background(), runoff.ID, "must-not-be-used", testNow.Add(featurevote.RunoffDuration+time.Hour))
	must(t, err)
	if final.Outcome != "tie_final" || final.RunoffID != 0 {
		t.Fatalf("final runoff=%#v", final)
	}
	active, err := store.ActiveFeatures(context.Background(), -1)
	must(t, err)
	if len(active) != 2 {
		t.Fatalf("active after final tie=%#v", active)
	}
}

package storage

import (
	"context"
	"testing"
	"time"

	"github.com/vaporon4a/movie-helper/internal/aiwork"
	"github.com/vaporon4a/movie-helper/internal/daily"
)

func TestAIWorkLeaseRecoveryAndPriorityBoost(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	now := testNow
	must(t, store.EnqueueAIWork(ctx, aiwork.FactRefill, -1, "fact:-1", aiwork.PriorityNormal, now, false))
	work, claimed, err := store.ClaimAIWork(ctx, now, time.Minute)
	must(t, err)
	if !claimed || work.Kind != aiwork.FactRefill || work.Attempts != 1 {
		t.Fatal(work, claimed)
	}
	_, claimed, err = store.ClaimAIWork(ctx, now, time.Minute)
	must(t, err)
	if claimed {
		t.Fatal("leased work claimed twice")
	}
	must(t, store.EnqueueAIWork(ctx, aiwork.FactRefill, -1, "fact:-1", aiwork.PriorityUrgent, now, true))
	_, claimed, err = store.ClaimAIWork(ctx, now, time.Minute)
	must(t, err)
	if claimed {
		t.Fatal("urgent request stole an active lease")
	}
	work, claimed, err = store.ClaimAIWork(ctx, now.Add(time.Minute), time.Minute)
	must(t, err)
	if !claimed || work.Attempts != 2 || work.Priority != aiwork.PriorityUrgent || work.LastErrorCode != "lease_expired" {
		t.Fatal(work, claimed)
	}
	must(t, store.CompleteAIWork(ctx, work.ID, now.Add(time.Minute)))
	must(t, store.EnqueueAIWork(ctx, aiwork.FactRefill, -1, "fact:-1", aiwork.PriorityNormal, now.Add(2*time.Minute), false))
	work, claimed, err = store.ClaimAIWork(ctx, now.Add(2*time.Minute), time.Minute)
	must(t, err)
	if !claimed || work.Attempts != 1 || work.Priority != aiwork.PriorityNormal {
		t.Fatal("completed recurring work was not reopened", work, claimed)
	}
}

func TestSeedAIWorkMaintainsStockAndExpiresMemes(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	must(t, store.EnsureChat(ctx, -1))
	must(t, store.SetZone(ctx, 1, -1, "UTC", testNow))
	must(t, store.SetSchedule(ctx, 2, -1, daily.Fact, "09:00", true, testNow))
	must(t, store.SetSchedule(ctx, 3, -1, daily.Meme, "10:00", true, testNow))
	must(t, store.SeedAIWork(ctx, testNow))

	seen := map[string]bool{}
	for range 2 {
		work, claimed, err := store.ClaimAIWork(ctx, testNow, time.Minute)
		must(t, err)
		if !claimed {
			t.Fatal("missing seeded content work")
		}
		seen[work.Kind] = true
		must(t, store.CompleteAIWork(ctx, work.ID, testNow))
	}
	if !seen[aiwork.FactRefill] || !seen[aiwork.MemeRefill] {
		t.Fatal(seen)
	}

	old := testNow.Add(-8 * 24 * time.Hour)
	_, err := store.SaveAIItems(ctx, -1, daily.Meme, []daily.Item{{Kind: daily.Meme, Image: "https://i.redd.it/old.jpg", Key: "old"}}, old)
	must(t, err)
	must(t, store.SeedAIWork(ctx, testNow))
	var state string
	must(t, store.db.QueryRow("SELECT state FROM items WHERE content_key='old'").Scan(&state))
	if state != "rejected" {
		t.Fatal("stale meme remained publishable", state)
	}
}

func TestSaveAIItemsHonorsModerationAndPreview(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	must(t, store.EnsureChat(ctx, -1))
	item := daily.Item{Kind: daily.Fact, Text: "Проверенный факт", Source: "https://example.org/fact", Key: "fact:one", GenerationPolicy: "fact-v4"}
	inserted, err := store.SaveAIItems(ctx, -1, daily.Fact, []daily.Item{item}, testNow)
	must(t, err)
	if inserted != 1 {
		t.Fatal(inserted)
	}
	preview, found, err := store.PreviewAIItem(ctx, -1, daily.Fact, testNow)
	must(t, err)
	if !found || preview.Key != item.Key {
		t.Fatal(preview, found)
	}
	must(t, store.SetModeration(ctx, 10, -1, true, testNow))
	_, found, err = store.PreviewAIItem(ctx, -1, daily.Fact, testNow)
	must(t, err)
	if found {
		t.Fatal("AI approval bypassed manual moderation")
	}
}

func TestAIStockRejectsStaleMemesAndOldFactPolicy(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	must(t, store.EnsureChat(ctx, -1))

	oldFact := daily.Item{Kind: daily.Fact, Text: "Старый факт", Source: "https://example.org/old", Key: "fact:old", GenerationPolicy: "fact-v3"}
	_, err := store.SaveAIItems(ctx, -1, daily.Fact, []daily.Item{oldFact}, testNow)
	must(t, err)
	count, err := store.AIStock(ctx, -1, daily.Fact, testNow)
	must(t, err)
	if count != 0 {
		t.Fatal("old fact policy counted as stock", count)
	}
	_, found, err := store.PreviewAIItem(ctx, -1, daily.Fact, testNow)
	must(t, err)
	if found {
		t.Fatal("old fact policy available for preview")
	}

	oldMeme := daily.Item{Kind: daily.Meme, Image: "https://i.redd.it/stale.jpg", Key: "meme:stale"}
	_, err = store.SaveAIItems(ctx, -1, daily.Meme, []daily.Item{oldMeme}, testNow.Add(-8*24*time.Hour))
	must(t, err)
	approved, err := store.HasApproved(ctx, -1, daily.Meme, testNow)
	must(t, err)
	if approved {
		t.Fatal("stale meme available for reservation")
	}
}

func TestReservedAIMemeCannotBeSentAfterTTL(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	must(t, store.EnsureChat(ctx, -1))
	must(t, store.SetZone(ctx, 1, -1, "UTC", testNow))
	must(t, store.SetSchedule(ctx, 2, -1, daily.Meme, "09:00", true, testNow))
	meme := daily.Item{Kind: daily.Meme, Image: "https://i.redd.it/almost-stale.jpg", Key: "meme:almost-stale"}
	_, err := store.SaveAIItems(ctx, -1, daily.Meme, []daily.Item{meme}, testNow.Add(-6*24*time.Hour-23*time.Hour))
	must(t, err)
	schedules, err := store.Schedules(ctx, -1)
	must(t, err)
	var schedule daily.Schedule
	for _, candidate := range schedules {
		if candidate.Kind == daily.Meme {
			schedule = candidate
		}
	}
	deliveryID, err := store.Reserve(ctx, schedule, "2026-09-22", testNow.Add(time.Minute).Unix(), testNow.Add(3*time.Hour).Unix())
	must(t, err)
	must(t, store.Attach(ctx, deliveryID, nil, testNow))
	claimed, err := store.Claim(ctx, deliveryID, testNow.Add(2*time.Hour))
	must(t, err)
	if claimed {
		t.Fatal("stale reserved AI meme entered Telegram delivery")
	}
}

func TestSeedBoostsTitleBeforeUpcomingVoteAndManualRefillDoesNotDeadlock(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	must(t, store.EnsureChat(ctx, -1))
	must(t, store.SetZone(ctx, 1, -1, "UTC", testNow))
	clock := testNow.Add(12 * time.Hour).Format("15:04")
	must(t, store.SetFeatureSchedule(ctx, 2, -1, int(testNow.Weekday()), clock, true, testNow))
	idea, err := store.AddFeature(ctx, 3, -1, 42, "Добавить удобный поиск фильмов по нескольким параметрам", "hash-upcoming", testNow)
	must(t, err)
	must(t, store.SeedAIWork(ctx, testNow))

	work, claimed, err := store.ClaimAIWork(ctx, testNow, time.Minute)
	must(t, err)
	if !claimed || work.Kind != aiwork.FeatureTitle || work.ScopeID != idea.ID || work.Priority != aiwork.PriorityUpcoming {
		t.Fatal(work, claimed)
	}
	must(t, store.CompleteAIWork(ctx, work.ID, testNow))

	refillCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	must(t, store.RequestAIRefill(refillCtx, -1, "titles", testNow.Add(time.Minute)))
	work, claimed, err = store.ClaimAIWork(ctx, testNow.Add(time.Minute), time.Minute)
	must(t, err)
	if !claimed || work.Priority != aiwork.PriorityUrgent {
		t.Fatal(work, claimed)
	}
}

package telegram

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"
	"github.com/vaporon4a/movie-helper/internal/daily"
	"github.com/vaporon4a/movie-helper/internal/feedback"
)

type feedbackStub struct {
	posts  []feedback.Post
	events []feedback.Event
	err    error
}

type reactionProviderFunc func(context.Context, string, int64) ([]daily.Item, error)

func (f reactionProviderFunc) Candidates(ctx context.Context, kind string, chat int64) ([]daily.Item, error) {
	return f(ctx, kind, chat)
}

func (s *feedbackStub) RegisterFeedbackPost(_ context.Context, post feedback.Post) error {
	s.posts = append(s.posts, post)
	return s.err
}

func (s *feedbackStub) RecordFeedback(_ context.Context, event feedback.Event) (bool, error) {
	s.events = append(s.events, event)
	return true, s.err
}

func emojiReaction(emoji string) models.ReactionType {
	return models.ReactionType{Type: models.ReactionTypeTypeEmoji, ReactionTypeEmoji: &models.ReactionTypeEmoji{Emoji: emoji}}
}

func reactionUpdate() *models.Update {
	return &models.Update{ID: 123, MessageReaction: &models.MessageReactionUpdated{
		Chat: models.Chat{ID: -1, Type: models.ChatTypeSupergroup}, MessageID: 99, Date: 100,
		User:        &models.User{ID: 12345, FirstName: "PRIVATE NAME"},
		OldReaction: []models.ReactionType{emojiReaction("👍")}, NewReaction: []models.ReactionType{emojiReaction("🤣")},
	}}
}

func TestReactionHandlerKeepsValuesWithoutActorIdentityOrReplies(t *testing.T) {
	h, api := handler(t)
	recorder := &feedbackStub{}
	h.Feedback = recorder
	now := time.Unix(101, 0)
	h.Now = func() time.Time { return now }
	h.Handle(t.Context(), nil, reactionUpdate())
	if len(recorder.events) != 1 || len(api.messages) != 0 {
		t.Fatal(recorder.events, api.messages)
	}
	event := recorder.events[0]
	if event.ChatID != -1 || event.MessageID != 99 || event.UpdateID != 123 || event.Mode != feedback.Delta || event.ReceivedAt != now || event.Before[0].Value != "👍" || event.After[0].Value != "🤣" {
		t.Fatal(event)
	}
	if err := event.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestReactionHandlerFiltersOtherChatsBotsPrivateChatsAndUnknownTypes(t *testing.T) {
	for _, change := range []func(*models.Update){
		func(u *models.Update) { u.MessageReaction.Chat.ID = -999 },
		func(u *models.Update) { u.MessageReaction.User.IsBot = true },
		func(u *models.Update) { u.MessageReaction.Chat.Type = models.ChatTypePrivate },
		func(u *models.Update) { u.MessageReaction.NewReaction = []models.ReactionType{{Type: "future-type"}} },
		func(u *models.Update) {
			u.MessageReaction.NewReaction = []models.ReactionType{{Type: models.ReactionTypeTypeEmoji}}
		},
	} {
		h, api := handler(t)
		recorder := &feedbackStub{}
		h.Feedback = recorder
		u := reactionUpdate()
		change(u)
		h.Handle(t.Context(), nil, u)
		if len(recorder.events) != 0 || len(api.messages) != 0 {
			t.Fatal(recorder.events, api.messages)
		}
	}
}

func TestReactionHandlerAnonymousSnapshotsAndCustomEmoji(t *testing.T) {
	h, api := handler(t)
	recorder := &feedbackStub{}
	h.Feedback = recorder
	custom := models.ReactionType{Type: models.ReactionTypeTypeCustomEmoji,
		ReactionTypeCustomEmoji: &models.ReactionTypeCustomEmoji{CustomEmojiID: "54321"}}
	u := &models.Update{ID: 124, MessageReactionCount: &models.MessageReactionCountUpdated{
		Chat: models.Chat{ID: -1, Type: models.ChatTypeSupergroup}, MessageID: 99, Date: 100,
		Reactions: []models.ReactionCount{{Type: custom, TotalCount: 4}, {Type: models.ReactionType{Type: models.ReactionTypeTypePaid}, TotalCount: 20}},
	}}
	h.Handle(t.Context(), nil, u)
	if len(recorder.events) != 1 || recorder.events[0].Mode != feedback.Snapshot || len(api.messages) != 0 {
		t.Fatal(recorder.events)
	}
	event := recorder.events[0]
	if event.After[0].Type != feedback.Custom || event.After[0].Value != "54321" || event.After[0].Count != 4 || event.After[1].Type != feedback.Paid {
		t.Fatal(event)
	}
	if err := event.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestConfirmedPublicationRegistersSnapshotAndStatsFailureNeverResends(t *testing.T) {
	api := &fakeAPI{}
	recorder := &feedbackStub{err: errors.New("database unavailable")}
	now := time.Unix(101, 0)
	sender := Sender{API: api, Feedback: recorder, Log: slog.New(slog.DiscardHandler), Now: func() time.Time { return now }}
	item := daily.Item{ID: 5, Kind: daily.Meme, Text: "Мем", Key: "meme:key", Image: "file-id", AIProvider: "gemini", AIModel: "old-model", GenerationPolicy: daily.MemePolicy}
	id, err := sender.Send(t.Context(), -1, item)
	if err != nil || id != 99 || len(api.photos) != 1 || len(recorder.posts) != 1 {
		t.Fatal(id, err, api.photos, recorder.posts)
	}
	post := recorder.posts[0]
	if post.Publication != feedback.Scheduled || post.PublishedAt != now || post.Item.AIModel != "old-model" || post.Item.ID != 5 {
		t.Fatal(post)
	}
	api.photoErr = errors.New("uncertain send")
	if _, err = sender.Send(t.Context(), -1, item); err == nil || len(recorder.posts) != 1 {
		t.Fatal("unconfirmed send registered", err, recorder.posts)
	}
}

func TestPreviewRegistersSeparatelyAndKeepsReadyItem(t *testing.T) {
	h, _ := handler(t)
	recorder := &feedbackStub{}
	h.Feedback = recorder
	appOf(t, h).provider = reactionProviderFunc(func(context.Context, string, int64) ([]daily.Item, error) {
		return []daily.Item{{ID: 7, Kind: daily.Fact, ChatID: -1, Text: "Готовый факт", Source: "https://example.org", Key: "fact:seven", AIModel: "prepared-model"}}, nil
	})
	h.Handle(t.Context(), nil, update(50, -1, 42, "/preview fact"))
	if len(recorder.posts) != 1 || recorder.posts[0].Publication != feedback.Preview || recorder.posts[0].Item.ID != 7 || recorder.posts[0].Item.AIModel != "prepared-model" {
		t.Fatal(recorder.posts)
	}
}

package telegram

import (
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"
	"github.com/vaporon4a/movie-helper/internal/clubprofile"
)

func TestProfileCommandsQuietResultAndLegacyButtons(t *testing.T) {
	h, api := handler(t)
	ctx := t.Context()
	store := appOf(t, h).store
	service, err := clubprofile.NewService(store, clubprofile.ProfileQuestions{History: store}, h.Allowed, h.Log, h.Now)
	if err != nil {
		t.Fatal(err)
	}
	h.Profile = service
	h.Handle(ctx, nil, update(201, -1, 42, "/reference_poll 5m"))
	sender, err := NewProfileSender(api, h.Log, h.Now)
	if err != nil {
		t.Fatal(err)
	}
	c, err := clubprofile.NewCoordinator(service, sender)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if len(api.polls) != 1 || api.polls[0].IsAnonymous == nil || !*api.polls[0].IsAnonymous || len(api.polls[0].Options) != 9 {
		t.Fatal(api.polls)
	}
	if api.polls[0].Question != "Какой из этих фильмов вам нравится больше?" {
		t.Fatal(api.polls[0].Question)
	}
	messageCount := len(api.messages)
	options := make([]models.PollOption, 9)
	options[0].VoterCount = 3
	options[8].VoterCount = 2
	h.Handle(ctx, nil, &models.Update{ID: 202, Poll: &models.Poll{ID: "poll-99", IsClosed: true, TotalVoterCount: 5, Options: options}})
	if err = c.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if len(api.messages) != messageCount || len(api.photos) != 0 || len(api.stops) != 0 {
		t.Fatal("closing produced messages")
	}
	polls, err := store.ProfilePolls(ctx, -1, clubprofile.Completed)
	if err != nil || len(polls) != 1 || polls[0].Result.UpdateID != 202 || *polls[0].Result.Total != 5 {
		t.Fatal(polls, err)
	}
	h.Handle(ctx, nil, update(203, -1, 43, "/movie_profile"))
	if !strings.Contains(api.messages[len(api.messages)-1].Text, "Ответов: 5") {
		t.Fatal(api.messages)
	}
	h.Handle(ctx, nil, &models.Update{ID: 204, CallbackQuery: &models.CallbackQuery{ID: "more", From: models.User{ID: 43}, Data: "movie_more:1", Message: models.MaybeInaccessibleMessage{Message: &models.Message{ID: 99, Chat: models.Chat{ID: -1}}}}})
	if len(api.photos) != 0 || !strings.Contains(api.messages[len(api.messages)-1].Text, "Подборки отключены") {
		t.Fatal("old page2 sent movies")
	}
}

func TestProfileTransportKeepsCompleteStopSnapshot(t *testing.T) {
	h, api := handler(t)
	now := time.Now()
	sender, err := NewProfileSender(api, h.Log, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	api.stopResult = &models.Poll{ID: "p", IsClosed: true, TotalVoterCount: 7, Options: []models.PollOption{{VoterCount: 2}, {VoterCount: 5}}}
	r, err := sender.CloseProfilePoll(t.Context(), clubprofile.Poll{ChatID: -1, MessageID: 77})
	if err != nil {
		t.Fatal(err)
	}
	if r.PollID != "p" || !r.Closed || r.Total == nil || *r.Total != 7 || r.Source != clubprofile.SourceStop || !r.ReceivedAt.Equal(now) {
		t.Fatal(r)
	}
}

func TestQueuedWatchCardDoesNotShowFalseDeadline(t *testing.T) {
	now := time.Date(2026, 10, 19, 8, 0, 0, 0, time.UTC)
	session := clubprofile.ReviewSession{ID: 1, WatchID: 1, State: "queued"}
	v := clubProfileView{now: now, sessions: []clubprofile.ReviewSession{session}, schedules: []clubprofile.Schedule{
		{ID: 1, Topic: clubprofile.Genre, Weekday: 1, Clock: "09:00", Zone: "UTC", Enabled: true},
		{ID: 2, Topic: clubprofile.Reference, Weekday: 1, Clock: "12:00", Zone: "UTC", Enabled: true},
		{ID: 3, Topic: clubprofile.Genre, Weekday: 3, Clock: "09:00", Zone: "UTC", Enabled: true},
		{ID: 4, Topic: clubprofile.Reference, Weekday: 3, Clock: "12:00", Zone: "UTC", Enabled: true},
	}}
	text := v.nextResearchText(session)
	if !strings.Contains(text, "19.10 09:00") || strings.Contains(text, "позже срока") {
		t.Fatal("unset deadline hides next slot", text)
	}
	session.State = "researching"
	session.FinishBefore = now.Add(30 * time.Minute)
	if text = v.nextResearchText(session); !strings.Contains(text, "28 дней с первого вопроса") {
		t.Fatal("started discussion deadline not explained", text)
	}
}

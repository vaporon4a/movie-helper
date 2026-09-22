package telegram

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/vaporon4a/movie-helper/internal/daily"
	"github.com/vaporon4a/movie-helper/internal/storage"
)

type fakeAPI struct {
	messages            []*bot.SendMessageParams
	photos              []*bot.SendPhotoParams
	adminErr, errorSend error
}

func (a *fakeAPI) SendMessage(_ context.Context, p *bot.SendMessageParams) (*models.Message, error) {
	a.messages = append(a.messages, p)
	return &models.Message{ID: 99}, a.errorSend
}
func (a *fakeAPI) SendPhoto(_ context.Context, p *bot.SendPhotoParams) (*models.Message, error) {
	a.photos = append(a.photos, p)
	return &models.Message{ID: 99}, a.errorSend
}
func (a *fakeAPI) GetChatAdministrators(context.Context, *bot.GetChatAdministratorsParams) ([]models.ChatMember, error) {
	return []models.ChatMember{{Type: models.ChatMemberTypeOwner, Owner: &models.ChatMemberOwner{User: &models.User{ID: 42}}}}, a.adminErr
}
func (a *fakeAPI) AnswerCallbackQuery(context.Context, *bot.AnswerCallbackQueryParams) (bool, error) {
	return true, nil
}
func handler(t *testing.T) (*Handler, *fakeAPI) {
	t.Helper()
	s, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	a := &fakeAPI{}
	return &Handler{Store: s, API: a, Allowed: map[int64]bool{-1: true, -2: true}, Username: "film_bot", Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: time.Now}, a
}
func update(id, chat, user int64, text string) *models.Update {
	return &models.Update{ID: id, Message: &models.Message{Text: text, Chat: models.Chat{ID: chat, Type: models.ChatTypeSupergroup}, From: &models.User{ID: user}}}
}
func TestAuthorizationChatIsolationAndReplay(t *testing.T) {
	h, a := handler(t)
	ctx := context.Background()
	h.Handle(ctx, nil, update(1, -1, 7, "/suggest_fact Миниатюры в кино | https://example.org/fact"))
	q, err := h.Store.Queue(ctx, -1, 0)
	if err != nil || len(q) != 1 {
		t.Fatal(q, err)
	}
	id := q[0].ID
	h.Handle(ctx, nil, update(1, -1, 7, "/suggest_fact Миниатюры в кино | https://example.org/fact"))
	q, _ = h.Store.Queue(ctx, -1, 0)
	if len(q) != 1 {
		t.Fatal("duplicate update added item")
	}
	h.Handle(ctx, nil, update(2, -1, 7, fmt.Sprintf("/approve %d", id)))
	item, _ := h.Store.Item(ctx, -1, id)
	if item.State != "pending" {
		t.Fatal("nonadmin approved")
	}
	// Even an administrator cannot approve an ID belonging to another chat.
	h.Handle(ctx, nil, update(3, -2, 42, fmt.Sprintf("/approve %d", id)))
	item, _ = h.Store.Item(ctx, -1, id)
	if item.State != "pending" {
		t.Fatal("cross-chat approval")
	}
	a.adminErr = errors.New("unavailable")
	h.Handle(ctx, nil, update(4, -1, 42, fmt.Sprintf("/approve %d", id)))
	item, _ = h.Store.Item(ctx, -1, id)
	if item.State != "pending" {
		t.Fatal("permissions fail open")
	}
	a.adminErr = nil
	h.Handle(ctx, nil, update(5, -1, 42, fmt.Sprintf("/approve %d", id)))
	item, _ = h.Store.Item(ctx, -1, id)
	if item.State != "approved" {
		t.Fatal("admin cannot approve")
	}
	before := len(a.messages)
	h.Handle(ctx, nil, update(6, -3, 42, "/suggest_fact text | https://example.org/fact"))
	h.Handle(ctx, nil, update(7, -1, 42, "/start@another_bot"))
	if len(a.messages) != before {
		t.Fatal("unrelated chat or bot responded")
	}
}
func TestCallbackScopeAndAnonymousAdmin(t *testing.T) {
	h, _ := handler(t)
	ctx := context.Background()
	h.Handle(ctx, nil, update(1, -1, 7, "/suggest_fact Текст | https://example.org"))
	q, _ := h.Store.Queue(ctx, -1, 0)
	id := q[0].ID
	h.Handle(ctx, nil, &models.Update{ID: 2, CallbackQuery: &models.CallbackQuery{ID: "q", From: models.User{ID: 42}, Data: fmt.Sprintf("approve:%d", id), Message: models.MaybeInaccessibleMessage{Message: &models.Message{Chat: models.Chat{ID: -2}}}}})
	item, _ := h.Store.Item(ctx, -1, id)
	if item.State != "pending" {
		t.Fatal("callback crossed chat")
	}
	u := update(3, -1, 42, fmt.Sprintf("/approve %d", id))
	u.Message.SenderChat = &models.Chat{ID: -1}
	h.Handle(ctx, nil, u)
	item, _ = h.Store.Item(ctx, -1, id)
	if item.State != "pending" {
		t.Fatal("anonymous approval")
	}
	h.Handle(ctx, nil, &models.Update{ID: 4, CallbackQuery: &models.CallbackQuery{ID: "q2", From: models.User{ID: 42}, Data: fmt.Sprintf("approve:%d", id), Message: models.MaybeInaccessibleMessage{Message: &models.Message{Chat: models.Chat{ID: -1}}}}})
	item, _ = h.Store.Item(ctx, -1, id)
	if item.State != "approved" {
		t.Fatal("valid callback not applied")
	}
}
func TestSenderAndErrors(t *testing.T) {
	a := &fakeAPI{}
	s := Sender{API: a}
	ctx := context.Background()
	if id, err := s.Send(ctx, -1, daily.Item{Kind: daily.Meme, Image: "telegram-file-id", Text: "Мем", Source: "https://redd.it/a"}); err != nil || id != 99 {
		t.Fatal(id, err)
	}
	if p := a.photos[0]; p.Photo.(*models.InputFileString).Data != "telegram-file-id" || !strings.Contains(p.Caption, "https://redd.it/a") {
		t.Fatal(p)
	}
	for _, tc := range []struct {
		err  error
		kind string
	}{{&bot.TooManyRequestsError{RetryAfter: 30}, "retry"}, {bot.ErrorForbidden, "forbidden"}, {bot.ErrorBadRequest, "permanent"}, {context.DeadlineExceeded, "unknown"}} {
		a.errorSend = tc.err
		_, err := s.Send(ctx, -1, daily.Item{Kind: daily.Fact, Text: "Факт", Source: "https://example.org"})
		var got *daily.SendError
		if !errors.As(err, &got) || got.Kind != tc.kind {
			t.Fatal(err)
		}
		if tc.kind == "retry" && got.After != 30*time.Second {
			t.Fatal("retry duration")
		}
	}
}

func TestBootstrapAllowsIDButNoGroupMutations(t *testing.T) {
	h, api := handler(t)
	h.Allowed = map[int64]bool{}
	h.Handle(context.Background(), nil, update(1, -123, 42, "/id@film_bot"))
	if len(api.messages) != 1 || api.messages[0].Text != "ID чата: -123" {
		t.Fatal("bootstrap must support discovery")
	}
	h.Handle(context.Background(), nil, update(2, -123, 42, "/timezone UTC"))
	h.Handle(context.Background(), nil, update(3, -123, 42, "/suggest_fact text | https://example.org"))
	if len(api.messages) != 1 {
		t.Fatal("bootstrap unexpectedly allowed group commands")
	}
	schedules, err := h.Store.Schedules(context.Background(), 0)
	if err != nil || len(schedules) != 0 {
		t.Fatal("bootstrap created group state", err)
	}
}

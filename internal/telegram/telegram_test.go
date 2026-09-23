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
	"github.com/vaporon4a/movie-helper/internal/gemini"
	"github.com/vaporon4a/movie-helper/internal/groq"
	"github.com/vaporon4a/movie-helper/internal/movieclub"
	"github.com/vaporon4a/movie-helper/internal/storage"
)

type fakeAPI struct {
	messages            []*bot.SendMessageParams
	photos              []*bot.SendPhotoParams
	polls               []*bot.SendPollParams
	stops               []*bot.StopPollParams
	mediaGroups         []*bot.SendMediaGroupParams
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
func (a *fakeAPI) SendPoll(_ context.Context, p *bot.SendPollParams) (*models.Message, error) {
	a.polls = append(a.polls, p)
	return &models.Message{ID: 99, Poll: &models.Poll{ID: "poll-99"}}, a.errorSend
}
func (a *fakeAPI) StopPoll(_ context.Context, p *bot.StopPollParams) (*models.Poll, error) {
	a.stops = append(a.stops, p)
	return &models.Poll{}, a.errorSend
}
func (a *fakeAPI) SendMediaGroup(_ context.Context, p *bot.SendMediaGroupParams) ([]*models.Message, error) {
	a.mediaGroups = append(a.mediaGroups, p)
	messages := make([]*models.Message, len(p.Media))
	for i := range messages {
		messages[i] = &models.Message{ID: 99 + i}
	}
	return messages, a.errorSend
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

func TestMovieCommandsCreateIsolatedRoundAndSchedule(t *testing.T) {
	h, _ := handler(t)
	h.MovieClub = &movieclub.Runner{Store: h.Store, Allowed: h.Allowed, Log: h.Log, Now: h.Now}
	ctx := context.Background()
	h.Handle(ctx, nil, update(100, -1, 42, "/timezone UTC"))
	h.Handle(ctx, nil, update(101, -1, 42, "/movie_schedule genre wed 19:00"))
	schedules, err := h.Store.MovieSchedules(ctx, -1)
	if err != nil || len(schedules) != 1 || schedules[0].Weekday != int(time.Wednesday) {
		t.Fatalf("schedules = %#v, %v", schedules, err)
	}
	h.Handle(ctx, nil, update(102, -1, 42, "/genre_poll 10m"))
	rounds, err := h.Store.LatestMovieRounds(ctx, -1)
	if err != nil || len(rounds) != 1 || rounds[0].State != movieclub.StatePlanned {
		t.Fatalf("rounds = %#v, %v", rounds, err)
	}
	// Replayed Telegram update must not start a second poll.
	h.Handle(ctx, nil, update(102, -1, 42, "/genre_poll 10m"))
	rounds, _ = h.Store.LatestMovieRounds(ctx, -1)
	if len(rounds) != 1 {
		t.Fatalf("replay created rounds: %#v", rounds)
	}
	if other, _ := h.Store.LatestMovieRounds(ctx, -2); len(other) != 0 {
		t.Fatalf("round crossed chat: %#v", other)
	}
}

func TestMovieSenderUsesAnonymousPollAndTenItemAlbum(t *testing.T) {
	api := &fakeAPI{}
	sender := MovieSender{API: api}
	labels := []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10"}
	if _, _, err := sender.OpenPoll(context.Background(), -1, labels, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if len(api.polls) != 1 || api.polls[0].IsAnonymous == nil || !*api.polls[0].IsAnonymous || !api.polls[0].AllowsRevoting {
		t.Fatalf("poll = %#v", api.polls)
	}
	movies := make([]movieclub.Recommendation, 10)
	for i := range movies {
		movies[i] = movieclub.Recommendation{Movie: movieclub.Movie{ID: int64(i + 1), Title: fmt.Sprintf("Фильм %d", i+1), PosterPath: fmt.Sprintf("/%d.jpg", i+1), Rating: 7}}
	}
	if _, err := sender.SendMovies(context.Background(), -1, movies, testCatalog{}); err != nil {
		t.Fatal(err)
	}
	if len(api.mediaGroups) != 1 || len(api.mediaGroups[0].Media) != 10 {
		t.Fatalf("media groups = %#v", api.mediaGroups)
	}
}

type testCatalog struct{}

func (testCatalog) Discover(context.Context, int64, int, int) ([]movieclub.Movie, error) {
	return nil, nil
}
func (testCatalog) PosterURL(path string) string { return "https://img.example" + path }

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

func TestModerationCommandPermissionsAndSettings(t *testing.T) {
	h, a := handler(t)
	ctx := context.Background()
	h.Handle(ctx, nil, update(1, -1, 7, "/moderation on"))
	rows, err := h.Store.Schedules(ctx, -1)
	if err != nil || rows[0].Moderation {
		t.Fatal("nonadmin changed mode", err)
	}
	a.adminErr = errors.New("offline")
	h.Handle(ctx, nil, update(2, -1, 42, "/moderation on"))
	rows, _ = h.Store.Schedules(ctx, -1)
	if rows[0].Moderation {
		t.Fatal("admin lookup failed open")
	}
	a.adminErr = nil
	h.Handle(ctx, nil, update(3, -1, 42, "/moderation on"))
	h.Handle(ctx, nil, update(4, -1, 7, "/settings"))
	if !strings.Contains(a.messages[len(a.messages)-1].Text, "Ручное одобрение включено") {
		t.Fatal("settings omitted moderation")
	}
	h.Handle(ctx, nil, update(5, -1, 42, "/moderation invalid"))
	rows, _ = h.Store.Schedules(ctx, -1)
	if !rows[0].Moderation {
		t.Fatal("invalid input changed mode")
	}
	h.Handle(ctx, nil, update(6, -1, 42, "/moderation off"))
	h.Handle(ctx, nil, update(7, -1, 7, "/settings"))
	if !strings.Contains(a.messages[len(a.messages)-1].Text, "Автоматический режим") {
		t.Fatal("settings omitted automatic mode")
	}
}

type previewProvider struct {
	remaining time.Duration
	calls     int
	empty     bool
	err       error
}

func (p *previewProvider) Candidates(ctx context.Context, kind string, chat int64) ([]daily.Item, error) {
	p.calls++
	if deadline, ok := ctx.Deadline(); ok {
		p.remaining = time.Until(deadline)
	}
	if p.empty || p.err != nil {
		return nil, p.err
	}
	return []daily.Item{{Kind: kind, ChatID: chat, Text: "Пробный материал", Image: "file-id", Source: "https://example.org", Key: "preview"}}, nil
}
func TestPreviewUsesGeminiProviderWithoutQueueOrSchedule(t *testing.T) {
	h, a := handler(t)
	p := &previewProvider{}
	h.Provider = p
	ctx := context.Background()
	for _, cmd := range []string{"/preview meme", "/preview fact"} {
		h.Handle(ctx, nil, update(1, -1, 7, cmd))
	}
	if p.calls != 0 {
		t.Fatal("nonadmin spent API budget")
	}
	h.Handle(ctx, nil, update(2, -3, 42, "/preview meme"))
	h.Handle(ctx, nil, update(3, -1, 42, "/preview invalid"))
	if p.calls != 0 {
		t.Fatal("invalid or unallowed preview fetched content")
	}
	h.Handle(ctx, nil, update(4, -1, 42, "/preview meme"))
	h.Handle(ctx, nil, update(5, -1, 42, "/preview fact"))
	if p.calls != 2 || len(a.photos) != 1 || !strings.Contains(a.photos[0].Caption, "Предпросмотр") || !strings.Contains(a.messages[len(a.messages)-1].Text, "Предпросмотр") {
		t.Fatal("preview not delivered")
	}
	if p.remaining < 230*time.Second {
		t.Fatal("handler deadline prevents fallback", p.remaining)
	}
	q, err := h.Store.Queue(ctx, -1, 0)
	if err != nil || len(q) != 0 {
		t.Fatal("preview queued item", q, err)
	}
	seen, err := h.Store.Seen(ctx, -1, daily.Meme, "preview")
	if err != nil || seen {
		t.Fatal("preview marked item published")
	}
	rows, err := h.Store.Schedules(ctx, -1)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Enabled || r.Moderation || r.Zone != "" {
			t.Fatal("preview changed settings", r)
		}
	}
	p.empty = true
	h.Handle(ctx, nil, update(6, -1, 42, "/preview meme"))
	if len(a.photos) != 1 || !strings.Contains(a.messages[len(a.messages)-1].Text, "Подходящего материала нет") {
		t.Fatal("empty result bypassed")
	}
	p.err = errors.New("upstream secret")
	h.Handle(ctx, nil, update(7, -1, 42, "/preview fact"))
	if strings.Contains(a.messages[len(a.messages)-1].Text, "upstream secret") || !strings.Contains(a.messages[len(a.messages)-1].Text, "Не удалось") {
		t.Fatal("unsafe error handling")
	}
}

func TestPreviewErrorsExplainModelAccessAndQuotas(t *testing.T) {
	for _, tc := range []struct {
		err          error
		reason, want string
	}{
		{&groq.HTTPError{Status: 429}, "groq_quota", "Groq"},
		{&groq.HTTPError{Status: 401}, "groq_access_denied", "GROQ_API_KEY"},
		{&groq.HTTPError{Status: 503}, "groq_unavailable", "503"},
		{gemini.ErrDailyLimit, "local_daily_limit", "00:00 UTC"},
		{&gemini.HTTPError{Status: 404}, "gemini_model_unavailable", "GEMINI_MODEL"},
		{&gemini.HTTPError{Status: 403}, "gemini_access_denied", "API-ключ"},
		{&gemini.HTTPError{Status: 429}, "gemini_quota", "квоты Google"},
		{&gemini.HTTPError{Status: 503}, "gemini_unavailable", "503"},
		{errors.New("private upstream body"), "source_or_generation_failed", "Не удалось"},
	} {
		message, reason := previewError(fmt.Errorf("wrapped: %w", tc.err))
		if reason != tc.reason || !strings.Contains(message, tc.want) || strings.Contains(message, "private") {
			t.Fatal(message, reason)
		}
	}
}

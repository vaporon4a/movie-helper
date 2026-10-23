package telegram

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/vaporon4a/movie-helper/internal/daily"
	"github.com/vaporon4a/movie-helper/internal/featurevote"
	"github.com/vaporon4a/movie-helper/internal/storage"
)

type fakeAPI struct {
	messages   []*bot.SendMessageParams
	edits      []*bot.EditMessageTextParams
	photos     []*bot.SendPhotoParams
	polls      []*bot.SendPollParams
	stops      []*bot.StopPollParams
	errorSend  error
	photoErr   error
	stopResult *models.Poll
	deletes    []*bot.DeleteMessageParams
	markups    []*bot.EditMessageReplyMarkupParams
	deleteErr  error
}

type testDailyApp struct {
	*daily.Service
	store    *storage.Store
	provider daily.Provider
}

func (a *testDailyApp) Candidates(ctx context.Context, kind string, chatID int64) ([]daily.Item, error) {
	if a.provider == nil {
		return nil, daily.ErrProviderDisabled
	}
	return a.provider.Candidates(ctx, kind, chatID)
}

func (a *fakeAPI) SendMessage(_ context.Context, p *bot.SendMessageParams) (*models.Message, error) {
	a.messages = append(a.messages, p)
	return &models.Message{ID: 99}, a.errorSend
}
func (a *fakeAPI) EditMessageText(_ context.Context, p *bot.EditMessageTextParams) (*models.Message, error) {
	a.edits = append(a.edits, p)
	return &models.Message{ID: p.MessageID}, a.errorSend
}
func (a *fakeAPI) SendPhoto(_ context.Context, p *bot.SendPhotoParams) (*models.Message, error) {
	a.photos = append(a.photos, p)
	if a.photoErr != nil {
		return &models.Message{ID: 99}, a.photoErr
	}
	return &models.Message{ID: 99}, a.errorSend
}
func (a *fakeAPI) SendPoll(_ context.Context, p *bot.SendPollParams) (*models.Message, error) {
	a.polls = append(a.polls, p)
	return &models.Message{ID: 99, Poll: &models.Poll{ID: "poll-99"}}, a.errorSend
}
func (a *fakeAPI) StopPoll(_ context.Context, p *bot.StopPollParams) (*models.Poll, error) {
	a.stops = append(a.stops, p)
	if a.stopResult != nil {
		return a.stopResult, a.errorSend
	}
	return &models.Poll{}, a.errorSend
}
func (a *fakeAPI) AnswerCallbackQuery(context.Context, *bot.AnswerCallbackQueryParams) (bool, error) {
	return true, nil
}

func (a *fakeAPI) DeleteMessage(_ context.Context, p *bot.DeleteMessageParams) (bool, error) {
	a.deletes = append(a.deletes, p)
	return a.deleteErr == nil, a.deleteErr
}

func (a *fakeAPI) EditMessageReplyMarkup(_ context.Context, p *bot.EditMessageReplyMarkupParams) (*models.Message, error) {
	a.markups = append(a.markups, p)
	return &models.Message{ID: p.MessageID}, a.errorSend
}
func handler(t *testing.T) (*Handler, *fakeAPI) {
	t.Helper()
	s, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	a := &fakeAPI{}
	service, err := daily.NewService(s, nil, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	app := &testDailyApp{Service: service, store: s}
	return &Handler{Daily: app, API: a, Allowed: map[int64]bool{-1: true, -2: true}, Username: "film_bot", Log: slog.New(slog.DiscardHandler), Now: time.Now}, a
}

func attachFeatureService(t *testing.T, h *Handler) *storage.Store {
	t.Helper()
	store := appOf(t, h).store
	service, err := featurevote.NewService(store, h.Log, h.Now, func() (string, error) { return "test-token", nil })
	if err != nil {
		t.Fatal(err)
	}
	h.Features = service
	return store
}
func update(id, chat, user int64, text string) *models.Update {
	return &models.Update{ID: id, Message: &models.Message{Text: text, Chat: models.Chat{ID: chat, Type: models.ChatTypeSupergroup}, From: &models.User{ID: user}}}
}
func TestAuthorizationChatIsolationAndReplay(t *testing.T) {
	h, a := handler(t)
	ctx := context.Background()
	h.Handle(ctx, nil, update(1, -1, 7, "/suggest_fact Миниатюры в кино | https://example.org/fact"))
	q, err := h.Daily.Queue(ctx, -1, 0)
	if err != nil || len(q) != 1 {
		t.Fatal(q, err)
	}
	id := q[0].ID
	h.Handle(ctx, nil, update(1, -1, 7, "/suggest_fact Миниатюры в кино | https://example.org/fact"))
	q, _ = h.Daily.Queue(ctx, -1, 0)
	if len(q) != 1 {
		t.Fatal("duplicate update added item")
	}
	// Чужая запись остаётся доступной только внутри её чата.
	h.Handle(ctx, nil, update(3, -2, 8, fmt.Sprintf("/approve %d", id)))
	item, _ := h.Daily.Item(ctx, -1, id)
	if item.State != "pending" {
		t.Fatal("cross-chat approval")
	}
	h.Handle(ctx, nil, update(4, -1, 8, fmt.Sprintf("/approve %d", id)))
	item, _ = h.Daily.Item(ctx, -1, id)
	if item.State != "approved" {
		t.Fatal("participant cannot approve another member's item")
	}
	before := len(a.messages)
	h.Handle(ctx, nil, update(6, -3, 42, "/suggest_fact text | https://example.org/fact"))
	h.Handle(ctx, nil, update(7, -1, 42, "/start@another_bot"))
	if len(a.messages) != before {
		t.Fatal("unrelated chat or bot responded")
	}
}

func TestFeatureIdeaForceReplyAndParticipantCommands(t *testing.T) {
	h, api := handler(t)
	attachFeatureService(t, h)
	ctx := context.Background()
	h.Handle(ctx, nil, update(800, -1, 7, "/idea"))
	if len(api.messages) != 1 || api.messages[0].Text != ideaPrompt {
		t.Fatalf("prompt=%#v", api.messages)
	}
	if _, ok := api.messages[0].ReplyMarkup.(*models.ForceReply); !ok {
		t.Fatalf("reply markup=%#v", api.messages[0].ReplyMarkup)
	}
	reply := update(801, -1, 7, "Добавить общий список просмотренных фильмов для участников")
	reply.Message.ReplyToMessage = &models.Message{Text: ideaPrompt}
	h.Handle(ctx, nil, reply)
	h.Handle(ctx, nil, update(802, -1, 7, "/my_ideas"))
	if got := api.messages[len(api.messages)-1].Text; !strings.Contains(got, "#1") || !strings.Contains(got, "Добавить общий список") {
		t.Fatalf("ideas=%q", got)
	}
	h.Handle(ctx, nil, update(803, -1, 8, "/idea_cancel 1"))
	ideas, err := h.Features.Mine(ctx, -1, 7)
	if err != nil || len(ideas) != 0 {
		t.Fatalf("ideas=%#v err=%v", ideas, err)
	}
}

func TestFeaturePrivateVotingUsesPagedDetailsAndOneChoice(t *testing.T) {
	h, api := handler(t)
	store := attachFeatureService(t, h)
	ctx := context.Background()
	if err := store.EnsureChat(ctx, -1); err != nil {
		t.Fatal(err)
	}
	mustIdea := func(op, author int64, text, hash string) featurevote.Idea {
		idea, err := store.AddFeature(ctx, op, -1, author, text, hash, h.Now())
		if err != nil {
			t.Fatal(err)
		}
		return idea
	}
	first := mustIdea(810, 7, "Добавить общий список просмотренных фильмов", "first-private")
	second := mustIdea(811, 8, "Добавить напоминания перед началом киновечера", "second-private")
	roundID, err := store.StartFeatureRound(ctx, 812, -1, h.Now(), time.Hour, "private-token")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.SaveFeatureRoundOptions(ctx, roundID, []featurevote.Option{{IdeaID: first.ID, Title: "Общий список просмотренных фильмов", Text: first.Text}, {IdeaID: second.ID, Title: "Напоминания перед началом киновечера", Text: second.Text}}); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimFeatureRound(ctx, roundID, featurevote.RoundPlanned, featurevote.RoundOpening, h.Now())
	if err != nil || !claimed {
		t.Fatal(claimed, err)
	}
	if err = store.OpenFeatureRound(ctx, roundID, featurevote.OpenResult{MessageID: 90, Mode: featurevote.BallotPrivate}, h.Now()); err != nil {
		t.Fatal(err)
	}
	private := &models.Update{ID: 813, Message: &models.Message{Text: "/start ideas_private-token", Chat: models.Chat{ID: 7, Type: models.ChatTypePrivate}, From: &models.User{ID: 7}}}
	h.Handle(ctx, nil, private)
	if got := api.messages[len(api.messages)-1]; !strings.Contains(got.Text, "Идеи для следующей функции") || got.ReplyMarkup == nil {
		t.Fatalf("private list=%#v", got)
	}
	callbackMessage := &models.Message{ID: 500, Chat: models.Chat{ID: 7, Type: models.ChatTypePrivate}}
	h.Handle(ctx, nil, &models.Update{CallbackQuery: &models.CallbackQuery{ID: "detail", From: models.User{ID: 7}, Data: fmt.Sprintf("fv:d:private-token:%d:0", first.ID), Message: models.MaybeInaccessibleMessage{Message: callbackMessage}}})
	if len(api.edits) == 0 || !strings.Contains(api.edits[len(api.edits)-1].Text, first.Text) {
		t.Fatalf("detail edits=%#v", api.edits)
	}
	h.Handle(ctx, nil, &models.Update{CallbackQuery: &models.CallbackQuery{ID: "vote", From: models.User{ID: 7}, Data: fmt.Sprintf("fv:v:private-token:%d:0", first.ID), Message: models.MaybeInaccessibleMessage{Message: callbackMessage}}})
	view, err := store.FeatureView(ctx, "private-token", 7)
	if err != nil || view.SelectedID != first.ID {
		t.Fatalf("view=%#v err=%v", view, err)
	}
}

func TestFeatureClosedPollUpdatePersistsNativeVotes(t *testing.T) {
	h, _ := handler(t)
	store := attachFeatureService(t, h)
	ctx := context.Background()
	if err := store.EnsureChat(ctx, -1); err != nil {
		t.Fatal(err)
	}
	mustIdea := func(op, author int64, text, hash string) featurevote.Idea {
		idea, err := store.AddFeature(ctx, op, -1, author, text, hash, h.Now())
		if err != nil {
			t.Fatal(err)
		}
		return idea
	}
	first := mustIdea(820, 7, "Добавить общий список просмотренных фильмов", "poll-first")
	second := mustIdea(821, 8, "Добавить напоминания перед началом киновечера", "poll-second")
	roundID, err := store.StartFeatureRound(ctx, 822, -1, h.Now(), time.Hour, "poll-token")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.SaveFeatureRoundOptions(ctx, roundID, []featurevote.Option{{IdeaID: first.ID, Title: "Общий список просмотренных фильмов", Text: first.Text}, {IdeaID: second.ID, Title: "Напоминания перед началом киновечера", Text: second.Text}}); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimFeatureRound(ctx, roundID, featurevote.RoundPlanned, featurevote.RoundOpening, h.Now())
	if err != nil || !claimed {
		t.Fatal(claimed, err)
	}
	if err = store.OpenFeatureRound(ctx, roundID, featurevote.OpenResult{MessageID: 91, PollID: "feature-poll", Mode: featurevote.BallotNative}, h.Now()); err != nil {
		t.Fatal(err)
	}
	h.Handle(ctx, nil, &models.Update{Poll: &models.Poll{ID: "feature-poll", IsClosed: true, Options: []models.PollOption{{VoterCount: 4}, {VoterCount: 2}}}})
	options, err := store.FeatureRoundOptions(ctx, roundID)
	if err != nil || len(options) != 2 || options[0].Votes != 4 || options[1].Votes != 2 {
		t.Fatalf("options=%#v err=%v", options, err)
	}
}

func TestFeatureSenderPublishesDeepLinkAndEscapedWinner(t *testing.T) {
	api := &fakeAPI{}
	sender := FeatureSender{API: api, Username: "film_bot"}
	round := featurevote.Round{ID: 1, ChatID: -1, ClosesAt: time.Now().Add(time.Hour).Unix(), Options: []featurevote.Option{{IdeaID: 9, Title: "Список <фильмов>", Text: "Полный текст & детали"}}}
	if _, err := sender.OpenRound(context.Background(), round, "ideas_token"); err != nil {
		t.Fatal(err)
	}
	keyboard := testValue[*models.InlineKeyboardMarkup](t, api.messages[0].ReplyMarkup)
	if got := keyboard.InlineKeyboard[0][0].URL; got != "https://t.me/film_bot?start=ideas_token" {
		t.Fatalf("url=%q", got)
	}
	round.Outcome, round.WinnerID = "winner", 9
	if err := sender.SendResult(context.Background(), round); err != nil {
		t.Fatal(err)
	}
	if got := api.messages[1].Text; !strings.Contains(got, "Список &lt;фильмов&gt;") || !strings.Contains(got, "Полный текст &amp; детали") {
		t.Fatalf("winner=%q", got)
	}
}

func TestFeatureSenderUsesNativePollWithDetailsButton(t *testing.T) {
	api := &fakeAPI{stopResult: &models.Poll{Options: []models.PollOption{{VoterCount: 3}, {VoterCount: 1}}}}
	sender := FeatureSender{API: api, Username: "film_bot"}
	round := featurevote.Round{ID: 2, ChatID: -1, Token: "native-token", ClosesAt: time.Now().Add(time.Hour).Unix(), Options: []featurevote.Option{
		{IdeaID: 9, Title: "Общий список просмотренных фильмов"},
		{IdeaID: 10, Title: "Напоминания перед началом киновечера"},
	}}
	opened, err := sender.OpenRound(context.Background(), round, "ideas_native-token")
	if err != nil || opened.Mode != featurevote.BallotNative || opened.PollID != "poll-99" || len(api.polls) != 1 {
		t.Fatalf("opened=%#v polls=%d err=%v", opened, len(api.polls), err)
	}
	keyboard := testValue[*models.InlineKeyboardMarkup](t, api.polls[0].ReplyMarkup)
	if got := keyboard.InlineKeyboard[0][0].URL; got != "https://t.me/film_bot?start=ideas_native-token" {
		t.Fatalf("details url=%q", got)
	}
	round.MessageID, round.PollID, round.BallotMode = int64(opened.MessageID), opened.PollID, opened.Mode
	votes, err := sender.CloseRound(context.Background(), round)
	if err != nil || !slices.Equal(votes, []int{3, 1}) || len(api.stops) != 1 {
		t.Fatalf("votes=%v stops=%d err=%v", votes, len(api.stops), err)
	}
}

func TestFeatureSenderFallsBackAboveTelegramPollLimit(t *testing.T) {
	api := &fakeAPI{}
	sender := FeatureSender{API: api, Username: "film_bot"}
	round := featurevote.Round{ChatID: -1, ClosesAt: time.Now().Add(time.Hour).Unix()}
	for i := range featurevote.NativePollMaxOptions + 1 {
		round.Options = append(round.Options, featurevote.Option{IdeaID: int64(i + 1), Title: fmt.Sprintf("Идея номер %d для голосования", i+1)})
	}
	opened, err := sender.OpenRound(context.Background(), round, "ideas_large")
	if err != nil || opened.Mode != featurevote.BallotPrivate || len(api.messages) != 1 || len(api.polls) != 0 {
		t.Fatalf("opened=%#v messages=%d polls=%d err=%v", opened, len(api.messages), len(api.polls), err)
	}
}
func TestCallbackScopeAndSenderIdentity(t *testing.T) {
	h, _ := handler(t)
	ctx := context.Background()
	h.Handle(ctx, nil, update(1, -1, 7, "/suggest_fact Текст | https://example.org"))
	q, _ := h.Daily.Queue(ctx, -1, 0)
	id := q[0].ID
	h.Handle(ctx, nil, &models.Update{ID: 2, CallbackQuery: &models.CallbackQuery{ID: "q", From: models.User{ID: 42}, Data: fmt.Sprintf("approve:%d", id), Message: models.MaybeInaccessibleMessage{Message: &models.Message{Chat: models.Chat{ID: -2}}}}})
	item, _ := h.Daily.Item(ctx, -1, id)
	if item.State != "pending" {
		t.Fatal("callback crossed chat")
	}
	u := update(3, -1, 42, fmt.Sprintf("/approve %d", id))
	u.Message.SenderChat = &models.Chat{ID: -1}
	h.Handle(ctx, nil, u)
	item, _ = h.Daily.Item(ctx, -1, id)
	if item.State != "pending" {
		t.Fatal("anonymous approval")
	}
	h.Handle(ctx, nil, &models.Update{ID: 4, CallbackQuery: &models.CallbackQuery{ID: "q2", From: models.User{ID: 8}, Data: fmt.Sprintf("approve:%d", id), Message: models.MaybeInaccessibleMessage{Message: &models.Message{Chat: models.Chat{ID: -1}}}}})
	item, _ = h.Daily.Item(ctx, -1, id)
	if item.State != "approved" {
		t.Fatal("valid callback not applied")
	}
}
func TestSenderAndErrors(t *testing.T) {
	a := &fakeAPI{}
	s := Sender{API: a}
	ctx := context.Background()
	if id, err := s.Send(ctx, -1, daily.Item{Kind: daily.Meme, Image: "telegram-file-id", Text: "Предпросмотр · <b>Мем</b> & шутка", Source: "https://redd.it/a?x=1&y=2"}); err != nil || id != 99 {
		t.Fatal(id, err)
	}
	if p := a.photos[0]; testValue[*models.InputFileString](t, p.Photo).Data != "telegram-file-id" || p.ParseMode != models.ParseModeHTML ||
		!strings.Contains(p.Caption, "🎭 <b>Мем дня</b> · <i>предпросмотр</i>") ||
		!strings.Contains(p.Caption, "&lt;b&gt;Мем&lt;/b&gt; &amp; шутка") ||
		!strings.Contains(p.Caption, `href="https://redd.it/a?x=1&amp;y=2"`) || strings.Contains(p.Caption, "Предпросмотр ·") {
		t.Fatal(p)
	}
	if id, err := s.Send(ctx, -1, daily.Item{Kind: daily.Fact, Text: "Предпросмотр · <b>Факт</b> & подробности\n\n" + daily.WikipediaAttribution, Source: "https://en.wikipedia.org/w/index.php?oldid=1&x=2"}); err != nil || id != 99 {
		t.Fatal(id, err)
	}
	if p := a.messages[0]; p.ParseMode != models.ParseModeHTML || p.LinkPreviewOptions == nil || p.LinkPreviewOptions.IsDisabled == nil || !*p.LinkPreviewOptions.IsDisabled ||
		!strings.Contains(p.Text, "🎬 <b>Факт о кино</b> · <i>предпросмотр</i>") ||
		!strings.Contains(p.Text, "&lt;b&gt;Факт&lt;/b&gt; &amp; подробности") ||
		!strings.Contains(p.Text, `href="https://en.wikipedia.org/w/index.php?oldid=1&amp;x=2"`) ||
		!strings.Contains(p.Text, `href="https://creativecommons.org/licenses/by-sa/4.0/"`) ||
		strings.Contains(p.Text, "Источник:") || strings.Contains(p.Text, "Предпросмотр ·") {
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
	schedules, err := h.Daily.Schedules(context.Background(), 0)
	if err != nil || len(schedules) != 0 {
		t.Fatal("bootstrap created group state", err)
	}
}

func TestModerationCommandPermissionsAndSettings(t *testing.T) {
	h, a := handler(t)
	ctx := context.Background()
	h.Handle(ctx, nil, update(1, -1, 7, "/moderation on"))
	rows, err := h.Daily.Schedules(ctx, -1)
	if err != nil || !rows[0].Moderation {
		t.Fatal("participant cannot change mode", err)
	}
	h.Handle(ctx, nil, update(4, -1, 7, "/settings"))
	if !strings.Contains(a.messages[len(a.messages)-1].Text, "Ручное одобрение включено") {
		t.Fatal("settings omitted moderation")
	}
	h.Handle(ctx, nil, update(5, -1, 42, "/moderation invalid"))
	rows, _ = h.Daily.Schedules(ctx, -1)
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
	appOf(t, h).provider = p
	ctx := context.Background()
	h.Handle(ctx, nil, update(2, -3, 42, "/preview meme"))
	h.Handle(ctx, nil, update(3, -1, 42, "/preview invalid"))
	if p.calls != 0 {
		t.Fatal("invalid or unallowed preview fetched content")
	}
	h.Handle(ctx, nil, update(4, -1, 7, "/preview meme"))
	h.Handle(ctx, nil, update(5, -1, 8, "/preview fact"))
	if p.calls != 2 || len(a.photos) != 1 || !strings.Contains(a.photos[0].Caption, "предпросмотр") || !strings.Contains(a.messages[len(a.messages)-1].Text, "предпросмотр") {
		t.Fatal("preview not delivered")
	}
	if p.remaining < 230*time.Second {
		t.Fatal("handler deadline prevents fallback", p.remaining)
	}
	q, err := h.Daily.Queue(ctx, -1, 0)
	if err != nil || len(q) != 0 {
		t.Fatal("preview queued item", q, err)
	}
	seen, err := appOf(t, h).store.Seen(ctx, -1, daily.Meme, "preview")
	if err != nil || seen {
		t.Fatal("preview marked item published")
	}
	rows, err := h.Daily.Schedules(ctx, -1)
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
	if len(a.photos) != 1 || !strings.Contains(a.messages[len(a.messages)-1].Text, "фоновую подготовку") || strings.Contains(a.messages[len(a.messages)-1].Text, "ключ") {
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
		{&daily.PreviewError{Code: "groq_quota", Status: 429}, "groq_quota", "Groq"},
		{&daily.PreviewError{Code: "groq_access_denied", Status: 401}, "groq_access_denied", "GROQ_API_KEY"},
		{&daily.PreviewError{Code: "groq_unavailable", Status: 503}, "groq_unavailable", "503"},
		{&daily.PreviewError{Code: "local_daily_limit"}, "local_daily_limit", "00:00 UTC"},
		{&daily.PreviewError{Code: "gemini_model_unavailable", Status: 404}, "gemini_model_unavailable", "GEMINI_MODEL"},
		{&daily.PreviewError{Code: "gemini_access_denied", Status: 403}, "gemini_access_denied", "API-ключ"},
		{&daily.PreviewError{Code: "gemini_quota", Status: 429}, "gemini_quota", "автоматически"},
		{&daily.PreviewError{Code: "gemini_unavailable", Status: 503}, "gemini_unavailable", "503"},
		{errors.New("private upstream body"), "source_or_generation_failed", "Не удалось"},
	} {
		message, reason := previewError(fmt.Errorf("wrapped: %w", tc.err))
		if reason != tc.reason || !strings.Contains(message, tc.want) || strings.Contains(message, "private") {
			t.Fatal(message, reason)
		}
	}
	message, reason := previewError(&daily.PreviewError{Code: "groq_quota", Status: 429, After: 90 * time.Second})
	if reason != "groq_quota" || !strings.Contains(message, "через 2 мин.") {
		t.Fatal(message, reason)
	}
}

func TestSettingsShowsPreparationReasonAndConditionalResolveHelp(t *testing.T) {
	now := time.Date(2026, 9, 25, 5, 0, 0, 0, time.UTC)
	schedules := []daily.Schedule{{Kind: daily.Meme, Clock: "08:27", Zone: "Asia/Novosibirsk", Enabled: true}}
	issues := make([]daily.Delivery, 1, 2)
	issues[0] = daily.Delivery{ID: 7, Kind: daily.Meme, State: "skipped", FetchAttempts: 6, Error: "no_approved_candidate"}
	text := settingsText(schedules, issues, now)
	if !strings.Contains(text, "пропущен после 6/6") || !strings.Contains(text, "AI не одобрил") || strings.Contains(text, "/resolve") {
		t.Fatal(text)
	}
	issues = append(issues, daily.Delivery{ID: 8, Kind: daily.Fact, Date: "2026-09-25", State: "unknown"})
	text = settingsText(schedules, issues, now)
	if !strings.Contains(text, "unknown: проверьте чат") || !strings.Contains(text, "/resolve ID sent") {
		t.Fatal(text)
	}
}

func appOf(t *testing.T, h *Handler) *testDailyApp {
	t.Helper()
	app, ok := h.Daily.(*testDailyApp)
	if !ok {
		t.Fatalf("unexpected daily fixture: %T", h.Daily)
	}
	return app
}

func testValue[T any](t *testing.T, value any) T {
	t.Helper()
	result, ok := value.(T)
	if !ok {
		t.Fatalf("unexpected fixture: %T", value)
	}
	return result
}

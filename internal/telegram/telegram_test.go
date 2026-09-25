package telegram

import (
	"bytes"
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
	"github.com/vaporon4a/movie-helper/internal/movieclub"
	"github.com/vaporon4a/movie-helper/internal/storage"
)

type fakeAPI struct {
	messages            []*bot.SendMessageParams
	edits               []*bot.EditMessageTextParams
	photos              []*bot.SendPhotoParams
	polls               []*bot.SendPollParams
	stops               []*bot.StopPollParams
	mediaGroups         []*bot.SendMediaGroupParams
	adminErr, errorSend error
	photoErr            error
}

type testDailyApp struct {
	*daily.Service
	store    *storage.Store
	provider daily.Provider
}

type movieClubStub struct {
	summary       movieclub.Summary
	err           error
	chat, roundID int64
	calls         int
}

func (*movieClubStub) Start(context.Context, movieclub.Feature, int64, int64, time.Duration) (int64, error) {
	return 0, nil
}
func (*movieClubStub) SetSchedule(context.Context, movieclub.Feature, int64, int64, int, string, bool) error {
	return nil
}
func (*movieClubStub) PauseSchedules(context.Context, movieclub.Feature, int64, int64) error {
	return nil
}
func (*movieClubStub) Settings(context.Context, int64) (movieclub.SettingsView, error) {
	return movieclub.SettingsView{}, nil
}
func (*movieClubStub) PollClosed(context.Context, string, []int) error { return nil }
func (s *movieClubStub) More(_ context.Context, chat, roundID int64) (movieclub.Summary, error) {
	s.calls++
	s.chat, s.roundID = chat, roundID
	return s.summary, s.err
}
func (*movieClubStub) Resolve(context.Context, int64, int64, int64, movieclub.ResolveAction) error {
	return nil
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
	service, err := daily.NewService(s, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	app := &testDailyApp{Service: service, store: s}
	return &Handler{Daily: app, API: a, Allowed: map[int64]bool{-1: true, -2: true}, Username: "film_bot", Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: time.Now}, a
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
	h.Handle(ctx, nil, update(2, -1, 7, fmt.Sprintf("/approve %d", id)))
	item, _ := h.Daily.Item(ctx, -1, id)
	if item.State != "pending" {
		t.Fatal("nonadmin approved")
	}
	// Even an administrator cannot approve an ID belonging to another chat.
	h.Handle(ctx, nil, update(3, -2, 42, fmt.Sprintf("/approve %d", id)))
	item, _ = h.Daily.Item(ctx, -1, id)
	if item.State != "pending" {
		t.Fatal("cross-chat approval")
	}
	a.adminErr = errors.New("unavailable")
	h.Handle(ctx, nil, update(4, -1, 42, fmt.Sprintf("/approve %d", id)))
	item, _ = h.Daily.Item(ctx, -1, id)
	if item.State != "pending" {
		t.Fatal("permissions fail open")
	}
	a.adminErr = nil
	h.Handle(ctx, nil, update(5, -1, 42, fmt.Sprintf("/approve %d", id)))
	item, _ = h.Daily.Item(ctx, -1, id)
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
	h.Handle(ctx, nil, &models.Update{ID: 4, CallbackQuery: &models.CallbackQuery{ID: "q2", From: models.User{ID: 42}, Data: fmt.Sprintf("approve:%d", id), Message: models.MaybeInaccessibleMessage{Message: &models.Message{Chat: models.Chat{ID: -1}}}}})
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
	if p := a.photos[0]; p.Photo.(*models.InputFileString).Data != "telegram-file-id" || p.ParseMode != models.ParseModeHTML ||
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

func TestMovieCommandsCreateIsolatedRoundAndSchedule(t *testing.T) {
	h, api := handler(t)
	store := h.Daily.(*testDailyApp).store
	scenario, err := movieclub.NewGenreScenario(testCatalog{}, store)
	if err != nil {
		t.Fatal(err)
	}
	sender, err := NewMovieSender(api, testCatalog{}.PosterURL, h.Log)
	if err != nil {
		t.Fatal(err)
	}
	scenarios, err := movieclub.NewScenarioSet(scenario)
	if err != nil {
		t.Fatal(err)
	}
	h.MovieClub, err = movieclub.NewService(store, sender, scenarios, h.Log, h.Now)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	h.Handle(ctx, nil, update(100, -1, 42, "/timezone UTC"))
	h.Handle(ctx, nil, update(101, -1, 42, "/movie_schedule genre wed 19:00"))
	schedules, err := store.MovieSchedules(ctx, -1)
	if err != nil || len(schedules) != 1 || schedules[0].Weekday != int(time.Wednesday) {
		t.Fatalf("schedules = %#v, %v", schedules, err)
	}
	h.Handle(ctx, nil, update(102, -1, 42, "/genre_poll 10m"))
	rounds, err := store.LatestMovieRounds(ctx, -1)
	if err != nil || len(rounds) != 1 || rounds[0].State != movieclub.StatePlanned {
		t.Fatalf("rounds = %#v, %v", rounds, err)
	}
	// Replayed Telegram update must not start a second poll.
	h.Handle(ctx, nil, update(102, -1, 42, "/genre_poll 10m"))
	rounds, _ = store.LatestMovieRounds(ctx, -1)
	if len(rounds) != 1 {
		t.Fatalf("replay created rounds: %#v", rounds)
	}
	if other, _ := store.LatestMovieRounds(ctx, -2); len(other) != 0 {
		t.Fatalf("round crossed chat: %#v", other)
	}
}

func TestMovieSenderUsesAnonymousPollAndTenItemAlbum(t *testing.T) {
	api := &fakeAPI{}
	sender, err := NewMovieSender(api, testCatalog{}.PosterURL, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	labels := []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10"}
	if _, _, err = sender.OpenPoll(context.Background(), -1, movieclub.Genre, labels); err != nil {
		t.Fatal(err)
	}
	if len(api.polls) != 1 || api.polls[0].IsAnonymous == nil || !*api.polls[0].IsAnonymous || !api.polls[0].AllowsRevoting {
		t.Fatalf("poll = %#v", api.polls)
	}
	if api.polls[0].Question != "Какой жанр выбираем для следующего киновечера?" {
		t.Fatalf("genre question = %q", api.polls[0].Question)
	}
	if api.polls[0].CloseDate != 0 {
		t.Fatalf("poll has Telegram auto-close %d; coordinator must own closing", api.polls[0].CloseDate)
	}
	if _, _, err = sender.OpenPoll(context.Background(), -1, movieclub.Reference, labels); err != nil {
		t.Fatal(err)
	}
	if api.polls[1].Question != "Какой фильм взять за ориентир для следующей подборки?" {
		t.Fatalf("reference question = %q", api.polls[1].Question)
	}
	movies := make([]movieclub.Recommendation, 10)
	for i := range movies {
		movies[i] = movieclub.Recommendation{Movie: movieclub.Movie{ID: int64(i + 1), Title: fmt.Sprintf("Фильм %d", i+1), PosterPath: fmt.Sprintf("/%d.jpg", i+1), Rating: 7}}
	}
	if _, err = sender.SendSummary(context.Background(), -1, movieclub.Summary{Feature: movieclub.Genre, Winner: "Ужасы", Movies: movies, Total: 20}, 7, true); err != nil {
		t.Fatal(err)
	}
	if len(api.messages) != 1 || api.messages[0].ParseMode != models.ParseModeHTML ||
		!strings.Contains(api.messages[0].Text, "10 из 20 фильмов") || api.messages[0].ReplyMarkup == nil {
		t.Fatalf("summary message=%#v", api.messages)
	}
	if _, err := sender.SendMovies(context.Background(), -1, movies); err != nil {
		t.Fatal(err)
	}
	if len(api.mediaGroups) != 1 || len(api.mediaGroups[0].Media) != 10 {
		t.Fatalf("media groups = %#v", api.mediaGroups)
	}
}

func TestSelectionSummaryUsesFeatureCopy(t *testing.T) {
	summary := movieclub.Summary{Feature: movieclub.Reference, Winner: "Матрица (1999)", Movies: []movieclub.Recommendation{
		{Movie: movieclub.Movie{ID: 1, Title: "Похожий", Rating: 8}, Relation: "similar"},
		{Movie: movieclub.Movie{ID: 2, Title: "Режиссёрский", Rating: 7}, Relation: "director"},
	}}
	got := selectionSummary(summary, false)
	if !strings.HasPrefix(got, "🎬 <b>Фильм-ориентир: Матрица (1999)</b>") || !strings.Contains(got, "Похожи по настроению") || !strings.Contains(got, "Другие фильмы режиссёра") {
		t.Fatalf("summary = %q", got)
	}
}

func TestReferenceSummaryUsesWinnerPosterAndBoundedCaption(t *testing.T) {
	api := &fakeAPI{}
	sender, err := NewMovieSender(api, testCatalog{}.PosterURL, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	summary := movieclub.Summary{
		Feature: movieclub.Reference, Winner: "Матрица (1999)", Hero: movieclub.Movie{PosterPath: "/matrix.jpg"},
		Movies: []movieclub.Recommendation{{Movie: movieclub.Movie{ID: 1, Title: "Тёмный город", Rating: 7.3}, Relation: "similar"}}, Total: 1,
	}
	if _, err = sender.SendSummary(context.Background(), -1, summary, 7, false); err != nil {
		t.Fatal(err)
	}
	if len(api.photos) != 1 || len(api.messages) != 0 || api.photos[0].ParseMode != "" || len([]rune(api.photos[0].Caption)) > 1024 {
		t.Fatalf("photos=%#v messages=%#v", api.photos, api.messages)
	}
}

func TestReferenceSummaryFallsBackToTextWhenTelegramRejectsPoster(t *testing.T) {
	api := &fakeAPI{photoErr: fmt.Errorf("%w, failed to get HTTP URL content", bot.ErrorBadRequest)}
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	sender, err := NewMovieSender(api, testCatalog{}.PosterURL, log)
	if err != nil {
		t.Fatal(err)
	}
	summary := movieclub.Summary{
		Feature: movieclub.Reference, Winner: "Матрица (1999)", Hero: movieclub.Movie{PosterPath: "/matrix.jpg"},
		Movies: []movieclub.Recommendation{{Movie: movieclub.Movie{ID: 1, Title: "Тёмный город", Rating: 7.3}, Relation: "similar"}}, Total: 1,
	}
	if _, err = sender.SendSummary(context.Background(), -1, summary, 7, false); err != nil {
		t.Fatal(err)
	}
	if len(api.photos) != 1 || len(api.messages) != 1 || !strings.Contains(api.messages[0].Text, "Фильм-ориентир") {
		t.Fatalf("photos=%#v messages=%#v", api.photos, api.messages)
	}
	if api.messages[0].ParseMode != "" {
		t.Fatalf("fallback parse mode=%q", api.messages[0].ParseMode)
	}
	if got := logs.String(); !strings.Contains(got, "operation=winner_poster") || !strings.Contains(got, "reason=photo_url_fetch") || !strings.Contains(got, "telegram movie delivery fallback") {
		t.Fatalf("logs=%q", got)
	}
}

func TestReferencePlainSummaryAlwaysFitsTelegramCaption(t *testing.T) {
	movies := make([]movieclub.Recommendation, 10)
	for i := range movies {
		movies[i] = movieclub.Recommendation{
			Movie:    movieclub.Movie{ID: int64(i + 1), Title: strings.Repeat("Очень длинное название & ", 20), Year: 2000 + i, Rating: 7.5},
			Relation: "similar",
		}
	}
	text := referenceSummaryPlain(movieclub.Summary{Feature: movieclub.Reference, Winner: strings.Repeat("Матрица ", 50), Movies: movies, Total: 10}, 1024)
	if len([]rune(text)) > 1024 || strings.ContainsAny(text, "<>") {
		t.Fatalf("caption runes=%d text=%q", len([]rune(text)), text)
	}
}

func TestMovieResolveReportsRetryInsteadOfScheduleChange(t *testing.T) {
	h, api := handler(t)
	h.MovieClub = &movieClubStub{}
	h.Handle(context.Background(), nil, update(700, -1, 42, "/movie_resolve 6 retry"))
	if len(api.messages) == 0 || api.messages[len(api.messages)-1].Text != "Подборка #6 поставлена на повторную отправку." {
		t.Fatalf("messages=%#v", api.messages)
	}
}

func TestMovieMoreEditsOriginalSummaryWithoutSendingAgain(t *testing.T) {
	h, api := handler(t)
	movies := make([]movieclub.Recommendation, 20)
	for i := range movies {
		movies[i] = movieclub.Recommendation{Movie: movieclub.Movie{
			ID: int64(i + 1), Title: fmt.Sprintf("Фильм <%d> & друзья", i+1), Year: 1980 + i, Rating: 7.1,
		}}
	}
	stub := &movieClubStub{summary: movieclub.Summary{Feature: movieclub.Genre, Winner: "Ужасы & мистика", Movies: movies, Total: 20}}
	h.MovieClub = stub
	message := &models.Message{ID: 321, Chat: models.Chat{ID: -1}}
	h.Handle(context.Background(), nil, &models.Update{CallbackQuery: &models.CallbackQuery{
		ID: "more", From: models.User{ID: 7}, Data: "movie_more:42", Message: models.MaybeInaccessibleMessage{Message: message},
	}})
	if stub.calls != 1 || stub.chat != -1 || stub.roundID != 42 {
		t.Fatalf("more calls=%d chat=%d round=%d", stub.calls, stub.chat, stub.roundID)
	}
	if len(api.edits) != 1 {
		t.Fatalf("edits=%d", len(api.edits))
	}
	edit := api.edits[0]
	if edit.MessageID != 321 || edit.ChatID != int64(-1) || edit.ParseMode != models.ParseModeHTML {
		t.Fatalf("edit=%#v", edit)
	}
	if edit.LinkPreviewOptions == nil || edit.LinkPreviewOptions.IsDisabled == nil || !*edit.LinkPreviewOptions.IsDisabled {
		t.Fatal("link preview is enabled")
	}
	keyboard, ok := edit.ReplyMarkup.(*models.InlineKeyboardMarkup)
	if !ok || len(keyboard.InlineKeyboard) != 0 {
		t.Fatalf("reply markup=%#v", edit.ReplyMarkup)
	}
	if !strings.Contains(edit.Text, "20 фильмов разных эпох") || !strings.Contains(edit.Text, "20. ") ||
		!strings.Contains(edit.Text, "Ужасы &amp; мистика") || strings.Contains(edit.Text, "Фильм <1>") ||
		!strings.Contains(edit.Text, `href="https://www.themoviedb.org/movie/1"`) {
		t.Fatalf("edited text=%q", edit.Text)
	}
}

func TestSelectionSummaryKeepsTelegramMessageBounded(t *testing.T) {
	movies := make([]movieclub.Recommendation, 20)
	for i := range movies {
		movies[i] = movieclub.Recommendation{Movie: movieclub.Movie{ID: int64(i + 1), Title: strings.Repeat("&", 300), Year: 2000 + i, Rating: 8}}
	}
	text := selectionSummary(movieclub.Summary{Feature: movieclub.Genre, Winner: "Драма", Movies: movies, Total: 20}, false)
	if len([]rune(text)) > 4096 {
		t.Fatalf("summary length=%d", len([]rune(text)))
	}
	if !strings.Contains(text, "…") {
		t.Fatal("long titles were not shortened")
	}
}

type testCatalog struct{}

func (testCatalog) Discover(context.Context, movieclub.DiscoverQuery) ([]movieclub.Movie, error) {
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
	if err != nil || rows[0].Moderation {
		t.Fatal("nonadmin changed mode", err)
	}
	a.adminErr = errors.New("offline")
	h.Handle(ctx, nil, update(2, -1, 42, "/moderation on"))
	rows, _ = h.Daily.Schedules(ctx, -1)
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
	h.Daily.(*testDailyApp).provider = p
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
	seen, err := h.Daily.(*testDailyApp).store.Seen(ctx, -1, daily.Meme, "preview")
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
	if len(a.photos) != 1 || !strings.Contains(a.messages[len(a.messages)-1].Text, "Подходящего мема") || strings.Contains(a.messages[len(a.messages)-1].Text, "ключ") {
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
		{&daily.PreviewError{Code: "gemini_quota", Status: 429}, "gemini_quota", "квоты Google"},
		{&daily.PreviewError{Code: "gemini_unavailable", Status: 503}, "gemini_unavailable", "503"},
		{errors.New("private upstream body"), "source_or_generation_failed", "Не удалось"},
	} {
		message, reason := previewError(fmt.Errorf("wrapped: %w", tc.err))
		if reason != tc.reason || !strings.Contains(message, tc.want) || strings.Contains(message, "private") {
			t.Fatal(message, reason)
		}
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

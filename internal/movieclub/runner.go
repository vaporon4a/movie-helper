package movieclub

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	defaultPollDuration = 24 * time.Hour
	catchupWindow       = 6 * time.Hour
	recentWindow        = 90 * 24 * time.Hour
)

type Repository interface {
	SetMovieSchedule(context.Context, int64, int64, int, string, bool, time.Time) error
	PauseMovieSchedules(context.Context, int64, int64, time.Time) error
	MovieSchedules(context.Context, int64) ([]Schedule, error)
	ReserveMovieRound(context.Context, int64, int64, int64, []Option) (int64, error)
	StartMovieRound(context.Context, int64, int64, time.Time, time.Duration, []Option) (int64, error)
	MovieRound(context.Context, int64, int64) (Round, error)
	MovieRounds(context.Context, string) ([]Round, error)
	LatestMovieRounds(context.Context, int64) ([]Round, error)
	MovieOptions(context.Context, int64) ([]Option, error)
	ClaimMovieRound(context.Context, int64, string, string, time.Time) (bool, error)
	OpenMovieRound(context.Context, int64, string, int, time.Time, time.Time) error
	DeferMovieRound(context.Context, int64, string, string, time.Time, string) error
	SaveMoviePoll(context.Context, int64, []int) error
	SaveMoviePollByID(context.Context, string, []int) error
	SaveMovieSelection(context.Context, int64, string, string, []Recommendation) error
	MovieRecommendations(context.Context, int64, int) ([]Recommendation, error)
	RecentMovieIDs(context.Context, int64, time.Time) (map[int64]bool, error)
	SetMoviePublishStage(context.Context, int64, int, string) error
	ClaimMoviePage2(context.Context, int64, int64) (bool, error)
	FinishMoviePage2(context.Context, int64, int64, string) error
	ResolveMovieRound(context.Context, int64, int64, int64, string) error
	DisableMovieSchedules(context.Context, int64) error
}

type Transport interface {
	OpenPoll(context.Context, int64, []string, time.Time) (string, int, error)
	ClosePoll(context.Context, int64, int) ([]int, error)
	SendSummary(context.Context, int64, string, int64, bool) (int, error)
	SendMovies(context.Context, int64, []Recommendation, Catalog) ([]int, error)
}

type Runner struct {
	Store    Repository
	Telegram Transport
	Catalog  Catalog
	Allowed  map[int64]bool
	Log      *slog.Logger
	Now      func() time.Time
	mu       sync.Mutex
}

func (r *Runner) Run(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		if err := r.Tick(ctx); err != nil && ctx.Err() == nil {
			r.Log.Error("movieclub tick failed", "reason", safeReason(err))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (r *Runner) Tick(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Catalog == nil || r.Telegram == nil {
		return nil
	}
	now := r.Now()
	if err := r.reserveSchedules(ctx, now); err != nil {
		return err
	}
	if err := r.openPlanned(ctx, now); err != nil {
		return err
	}
	if err := r.closeDue(ctx, now); err != nil {
		return err
	}
	if err := r.prepareSelections(ctx, now); err != nil {
		return err
	}
	return r.publishReady(ctx, now)
}

func (r *Runner) Start(ctx context.Context, op, chat int64, duration time.Duration) (int64, error) {
	if duration < 5*time.Minute || duration > defaultPollDuration {
		return 0, errors.New("duration must be 5m..24h")
	}
	now := r.Now()
	options := GenreOptions(uint64(chat) ^ uint64(now.Unix()/60))
	id, err := r.Store.StartMovieRound(ctx, op, chat, now, duration, options)
	if err == nil {
		r.Log.Info("movieclub round planned", "round_id", id, "chat_id", chat, "feature", Genre, "manual", true)
	}
	return id, err
}

func (r *Runner) SetSchedule(ctx context.Context, op, chat int64, weekday int, clock string, enabled bool) error {
	return r.Store.SetMovieSchedule(ctx, op, chat, weekday, clock, enabled, r.Now())
}

func (r *Runner) PauseSchedules(ctx context.Context, op, chat int64) error {
	return r.Store.PauseMovieSchedules(ctx, op, chat, r.Now())
}

func (r *Runner) Settings(ctx context.Context, chat int64) (string, error) {
	schedules, err := r.Store.MovieSchedules(ctx, chat)
	if err != nil {
		return "", err
	}
	lines := []string{"Киноопросы по жанрам:"}
	if len(schedules) == 0 {
		lines = append(lines, "расписание выключено")
	}
	for _, schedule := range schedules {
		state := "выключено"
		if schedule.Enabled {
			state = "включено"
		}
		lines = append(lines, fmt.Sprintf("%s %s — %s", WeekdayName(schedule.Weekday), schedule.Clock, state))
	}
	rounds, err := r.Store.LatestMovieRounds(ctx, chat)
	if err != nil {
		return "", err
	}
	for _, round := range rounds {
		if round.State == StatePublished || round.State == StateCancelled || round.State == StateFailed {
			continue
		}
		line := fmt.Sprintf("Раунд #%d: %s", round.ID, round.State)
		if round.State == StateOpen {
			line += ", закрытие " + time.Unix(round.ClosesAt, 0).Format(time.RFC3339)
		}
		if round.ErrorCode != "" {
			line += ", ошибка " + round.ErrorCode
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n"), nil
}

func (r *Runner) PollClosed(ctx context.Context, pollID string, votes []int) error {
	if pollID == "" {
		return ErrConflict
	}
	err := r.Store.SaveMoviePollByID(ctx, pollID, votes)
	if err == nil {
		r.Log.Info("movieclub poll closed", "poll_id_present", true, "options", len(votes))
	}
	return err
}

func (r *Runner) More(ctx context.Context, chat, roundID int64) error {
	claimed, err := r.Store.ClaimMoviePage2(ctx, chat, roundID)
	if err != nil || !claimed {
		if err == nil {
			err = ErrConflict
		}
		return err
	}
	movies, err := r.Store.MovieRecommendations(ctx, roundID, 2)
	if err != nil {
		_ = r.Store.FinishMoviePage2(context.WithoutCancel(ctx), chat, roundID, "ready")
		return err
	}
	_, err = r.Telegram.SendMovies(ctx, chat, movies, r.Catalog)
	state := "sent"
	if err != nil {
		state = pageFailure(err)
	}
	persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if saveErr := r.Store.FinishMoviePage2(persist, chat, roundID, state); saveErr != nil {
		return saveErr
	}
	return err
}

func (r *Runner) Resolve(ctx context.Context, op, chat, roundID int64, action string) error {
	return r.Store.ResolveMovieRound(ctx, op, chat, roundID, action)
}

func (r *Runner) reserveSchedules(ctx context.Context, now time.Time) error {
	schedules, err := r.Store.MovieSchedules(ctx, 0)
	if err != nil {
		return err
	}
	for _, schedule := range schedules {
		slot, eligible, err := r.scheduleSlot(schedule, now)
		if err != nil {
			return err
		}
		if !eligible {
			continue
		}
		options := GenreOptions(uint64(schedule.ChatID) ^ uint64(slot.Unix()))
		id, err := r.Store.ReserveMovieRound(ctx, schedule.ChatID, slot.Unix(), slot.Add(defaultPollDuration).Unix(), options)
		if errors.Is(err, ErrActiveRound) || errors.Is(err, ErrDuplicate) || errors.Is(err, context.Canceled) {
			continue
		}
		if err != nil {
			return err
		}
		if id != 0 {
			r.Log.Info("movieclub round planned", "round_id", id, "chat_id", schedule.ChatID, "feature", Genre, "manual", false)
		}
	}
	return nil
}

func (r *Runner) scheduleSlot(schedule Schedule, now time.Time) (time.Time, bool, error) {
	if !schedule.Enabled || !r.Allowed[schedule.ChatID] || schedule.Zone == "" {
		return time.Time{}, false, nil
	}
	slot, err := WeeklySlot(now, schedule.Weekday, schedule.Clock, schedule.Zone)
	if err != nil {
		return time.Time{}, false, err
	}
	eligible := !now.Before(slot) && now.Before(slot.Add(catchupWindow)) && slot.Unix() > schedule.Effective
	return slot, eligible, nil
}

func (r *Runner) openPlanned(ctx context.Context, now time.Time) error {
	rounds, err := r.Store.MovieRounds(ctx, StatePlanned)
	if err != nil {
		return err
	}
	for _, round := range rounds {
		if err = r.openRound(ctx, round, now); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runner) openRound(ctx context.Context, round Round, now time.Time) error {
	if !r.Allowed[round.ChatID] || round.NextAttempt > now.Unix() {
		return nil
	}
	claimed, err := r.Store.ClaimMovieRound(ctx, round.ID, StatePlanned, StatePollCreating, now)
	if err != nil || !claimed {
		return err
	}
	options, err := r.Store.MovieOptions(ctx, round.ID)
	if err != nil {
		return err
	}
	labels := make([]string, len(options))
	for i, option := range options {
		labels[i] = option.Label
	}
	closes := time.Unix(round.ClosesAt, 0)
	pollID, messageID, sendErr := r.Telegram.OpenPoll(ctx, round.ChatID, labels, closes)
	if sendErr != nil {
		return r.handleFailure(ctx, round, StatePollCreating, sendErr, now)
	}
	if err = r.Store.OpenMovieRound(ctx, round.ID, pollID, messageID, now, closes); err != nil {
		return err
	}
	r.Log.Info("movieclub poll opened", "round_id", round.ID, "chat_id", round.ChatID, "options", len(options))
	return nil
}

func (r *Runner) closeDue(ctx context.Context, now time.Time) error {
	rounds, err := r.Store.MovieRounds(ctx, StateOpen)
	if err != nil {
		return err
	}
	for _, round := range rounds {
		if err = r.closeRound(ctx, round, now); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runner) closeRound(ctx context.Context, round Round, now time.Time) error {
	if round.ClosesAt > now.Unix() || round.NextAttempt > now.Unix() {
		return nil
	}
	claimed, err := r.Store.ClaimMovieRound(ctx, round.ID, StateOpen, StateClosing, now)
	if err != nil || !claimed {
		return err
	}
	votes, sendErr := r.Telegram.ClosePoll(ctx, round.ChatID, round.PollMessageID)
	if sendErr != nil {
		return r.handleFailure(ctx, round, StateClosing, sendErr, now)
	}
	if err = r.Store.SaveMoviePoll(ctx, round.ID, votes); err != nil {
		if errors.Is(err, ErrConflict) {
			return nil
		}
		return err
	}
	r.Log.Info("movieclub poll tally saved", "round_id", round.ID, "chat_id", round.ChatID, "options", len(votes))
	return nil
}

func (r *Runner) prepareSelections(ctx context.Context, now time.Time) error {
	rounds, err := r.Store.MovieRounds(ctx, StateSelecting)
	if err != nil {
		return err
	}
	for _, round := range rounds {
		if round.NextAttempt > now.Unix() {
			continue
		}
		options, err := r.Store.MovieOptions(ctx, round.ID)
		if err != nil {
			return err
		}
		winners := Winners(options, uint64(round.ID))
		if len(winners) == 0 {
			if err = r.Store.SaveMovieSelection(ctx, round.ID, "", "Опрос завершён без голосов — подборки сегодня не будет.", nil); err != nil {
				return err
			}
			continue
		}
		movies, err := r.selectMovies(ctx, round, winners, now)
		if err != nil {
			if deferErr := r.Store.DeferMovieRound(ctx, round.ID, StateSelecting, StateSelecting, now.Add(time.Hour), catalogReason(err)); deferErr != nil {
				return deferErr
			}
			continue
		}
		winnerNames := make([]string, len(winners))
		for i, winner := range winners {
			winnerNames[i] = winner.Label
		}
		winner := strings.Join(winnerNames, " + ")
		text := selectionSummary(winner, movies)
		if err = r.Store.SaveMovieSelection(ctx, round.ID, winner, text, movies); err != nil {
			return err
		}
		r.Log.Info("movieclub selection prepared", "round_id", round.ID, "chat_id", round.ChatID, "winners", len(winners), "movies", len(movies))
	}
	return nil
}

func (r *Runner) publishReady(ctx context.Context, now time.Time) error {
	rounds, err := r.Store.MovieRounds(ctx, StateReady)
	if err != nil {
		return err
	}
	for _, round := range rounds {
		if err = r.publishRound(ctx, round, now); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runner) publishRound(ctx context.Context, round Round, now time.Time) error {
	if round.NextAttempt > now.Unix() {
		return nil
	}
	claimed, err := r.Store.ClaimMovieRound(ctx, round.ID, StateReady, StatePublishing, now)
	if err != nil || !claimed {
		return err
	}
	page1, err := r.Store.MovieRecommendations(ctx, round.ID, 1)
	if err != nil {
		return err
	}
	if round.PublishStage == 0 {
		if err = r.publishSummary(ctx, round, now); err != nil {
			return err
		}
	}
	if len(page1) > 0 {
		if _, sendErr := r.Telegram.SendMovies(ctx, round.ChatID, page1, r.Catalog); sendErr != nil {
			round.PublishStage = 1
			return r.handleFailure(ctx, round, StatePublishing, sendErr, now)
		}
	}
	if err = r.Store.SetMoviePublishStage(ctx, round.ID, 2, StatePublished); err != nil {
		return err
	}
	r.Log.Info("movieclub selection published", "round_id", round.ID, "chat_id", round.ChatID, "movies", len(page1))
	return nil
}

func (r *Runner) publishSummary(ctx context.Context, round Round, now time.Time) error {
	if _, err := r.Telegram.SendSummary(ctx, round.ChatID, round.ResultText, round.ID, round.Page2State == "ready"); err != nil {
		return r.handleFailure(ctx, round, StatePublishing, err, now)
	}
	return r.Store.SetMoviePublishStage(ctx, round.ID, 1, StatePublishing)
}

func (r *Runner) selectMovies(ctx context.Context, round Round, winners []Option, now time.Time) ([]Recommendation, error) {
	recent, err := r.Store.RecentMovieIDs(ctx, round.ChatID, now.Add(-recentWindow))
	if err != nil {
		return nil, err
	}
	groups := make([][]Movie, len(winners))
	seen := make(map[int64]bool)
	for i, winner := range winners {
		groups[i], err = r.genreMovies(ctx, winner.ProviderID, 20/len(winners), recent, seen)
		if err != nil {
			return nil, err
		}
	}
	selected := interleaveMovies(groups, 20)
	out := make([]Recommendation, len(selected))
	for i, movie := range selected {
		out[i] = Recommendation{Movie: movie, RoundID: round.ID, Page: i/10 + 1, Position: i % 10, Relation: "top"}
	}
	return out, nil
}

func (r *Runner) genreMovies(ctx context.Context, genreID int64, limit int, recent, seen map[int64]bool) ([]Movie, error) {
	var movies []Movie
	for _, minVotes := range []int{300, 100} {
		for page := 1; page <= 2; page++ {
			batch, err := r.Catalog.Discover(ctx, genreID, page, minVotes)
			if err != nil {
				return nil, err
			}
			movies = appendNewMovies(movies, batch, recent, seen)
		}
		if len(movies) >= limit {
			break
		}
	}
	return movies, nil
}

func appendNewMovies(dst, batch []Movie, recent, seen map[int64]bool) []Movie {
	for _, movie := range batch {
		if !seen[movie.ID] && !recent[movie.ID] {
			seen[movie.ID] = true
			dst = append(dst, movie)
		}
	}
	return dst
}

func interleaveMovies(groups [][]Movie, limit int) []Movie {
	var selected []Movie
	for position := 0; len(selected) < limit; position++ {
		added := false
		for _, group := range groups {
			if position < len(group) && len(selected) < limit {
				selected = append(selected, group[position])
				added = true
			}
		}
		if !added {
			return selected
		}
	}
	return selected
}

func (r *Runner) handleFailure(ctx context.Context, round Round, from string, err error, now time.Time) error {
	typed, ok := errors.AsType[*DeliveryError](err)
	target, code, next := StateUnknown, "telegram_unknown", time.Time{}
	if ok {
		switch typed.Kind {
		case DeliveryRetry:
			target, code, next = retryTarget(from), "telegram_rate_limit", now.Add(max(typed.After, time.Second))
		case DeliveryForbidden:
			target, code = StateFailed, "telegram_forbidden"
			if disableErr := r.Store.DisableMovieSchedules(ctx, round.ChatID); disableErr != nil {
				return disableErr
			}
		case DeliveryPermanent:
			target, code = StateFailed, "telegram_rejected"
		}
	}
	r.Log.Warn("movieclub operation failed", "round_id", round.ID, "chat_id", round.ChatID, "from", from, "target", target, "reason", code)
	return r.Store.DeferMovieRound(context.WithoutCancel(ctx), round.ID, from, target, next, code)
}

func retryTarget(from string) string {
	switch from {
	case StatePollCreating:
		return StatePlanned
	case StateClosing:
		return StateOpen
	case StatePublishing:
		return StateReady
	default:
		return from
	}
}

func pageFailure(err error) string {
	typed, ok := errors.AsType[*DeliveryError](err)
	if ok && typed.Kind == DeliveryRetry {
		return "ready"
	}
	return "unknown"
}

func selectionSummary(winner string, movies []Recommendation) string {
	lines := []string{"🎬 Победил жанр: " + winner, ""}
	for i, movie := range movies {
		if i == 10 {
			break
		}
		year := ""
		if movie.Year != 0 {
			year = fmt.Sprintf(" (%d)", movie.Year)
		}
		lines = append(lines, fmt.Sprintf("%d. %s%s — %.1f", i+1, movie.Title, year, movie.Rating))
	}
	if len(movies) == 0 {
		lines = append(lines, "TMDB не вернул подходящих фильмов.")
	}
	lines = append(lines, "", "Данные и изображения: TMDB. This product uses the TMDB API but is not endorsed or certified by TMDB.")
	return strings.Join(lines, "\n")
}

func safeReason(err error) string {
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	return "internal"
}

func catalogReason(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "tmdb_timeout"
	}
	return "tmdb_unavailable"
}

func WeekdayName(value int) string {
	names := []string{"вс", "пн", "вт", "ср", "чт", "пт", "сб"}
	if value < 0 || value >= len(names) {
		return "?"
	}
	return names[value]
}

func ParseWeekday(value string) (int, bool) {
	days := map[string]int{"sun": 0, "вс": 0, "mon": 1, "пн": 1, "tue": 2, "вт": 2, "wed": 3, "ср": 3, "thu": 4, "чт": 4, "fri": 5, "пт": 5, "sat": 6, "сб": 6}
	day, ok := days[strings.ToLower(strings.TrimSpace(value))]
	return day, ok
}

func WeeklySlot(now time.Time, weekday int, clock, zone string) (time.Time, error) {
	loc, err := time.LoadLocation(zone)
	if err != nil || weekday < 0 || weekday > 6 {
		return time.Time{}, errors.New("invalid weekly schedule")
	}
	parsed, err := time.Parse("15:04", clock)
	if err != nil {
		return time.Time{}, err
	}
	local := now.In(loc)
	diff := weekday - int(local.Weekday())
	target := local.AddDate(0, 0, diff)
	y, m, d := target.Date()
	anchor := time.Date(y, m, d, 12, 0, 0, 0, loc)
	want := parsed.Hour()*60 + parsed.Minute()
	for candidate := anchor.Add(-18 * time.Hour); !candidate.After(anchor.Add(18 * time.Hour)); candidate = candidate.Add(time.Minute) {
		value := candidate.In(loc)
		vy, vm, vd := value.Date()
		if vy == y && vm == m && vd == d && value.Hour()*60+value.Minute() >= want {
			return candidate, nil
		}
	}
	return time.Time{}, errors.New("no slot on local day")
}

func SortMovies(movies []Movie) {
	sort.SliceStable(movies, func(i, j int) bool {
		if movies[i].Popularity != movies[j].Popularity {
			return movies[i].Popularity > movies[j].Popularity
		}
		return movies[i].VoteCount > movies[j].VoteCount
	})
}

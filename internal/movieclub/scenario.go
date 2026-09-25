package movieclub

import (
	"context"
	"errors"
	"math/rand/v2"
	"sync"
	"time"
)

var ErrUnknownFeature = errors.New("movie poll feature is not configured")

type ScenarioSet map[Feature]Scenario

func NewScenarioSet(scenarios ...Scenario) (ScenarioSet, error) {
	set := make(ScenarioSet, len(scenarios))
	for _, scenario := range scenarios {
		if scenario == nil || !scenario.Feature().Valid() || set[scenario.Feature()] != nil {
			return nil, ErrUnknownFeature
		}
		set[scenario.Feature()] = scenario
	}
	if len(set) == 0 {
		return nil, ErrUnknownFeature
	}
	return set, nil
}

type GenreScenario struct {
	Catalog Catalog
	History RecommendationHistory
}

func NewGenreScenario(catalog Catalog, history RecommendationHistory) (*GenreScenario, error) {
	if catalog == nil || history == nil {
		return nil, errors.New("movie genre scenario requires catalog and history")
	}
	return &GenreScenario{Catalog: catalog, History: history}, nil
}

func (*GenreScenario) Feature() Feature { return Genre }
func (*GenreScenario) Options(_ context.Context, _ int64, seed uint64, _ time.Time) ([]Option, error) {
	return GenreOptions(seed), nil
}
func (*GenreScenario) Winners(options []Option, seed uint64) []Option {
	return Winners(options, seed)
}

func (s *GenreScenario) Recommendations(ctx context.Context, round Round, winners []Option, now time.Time) (Movie, []Recommendation, error) {
	recent, err := s.History.RecentMovieIDs(ctx, round.ChatID, now.Add(-recentWindow))
	if err != nil {
		return Movie{}, nil, err
	}
	groups := make([][]Movie, len(winners))
	seen := make(map[int64]bool)
	for i, winner := range winners {
		groups[i], err = s.genreMovies(ctx, round.ID, winner.ProviderID, 20/len(winners), recent, seen, now)
		if err != nil {
			return Movie{}, nil, err
		}
	}
	selected := interleaveMovies(groups, 20)
	out := make([]Recommendation, len(selected))
	for i, movie := range selected {
		out[i] = Recommendation{Movie: movie, RoundID: round.ID, Page: i/10 + 1, Position: i % 10, Relation: "top"}
	}
	return Movie{}, out, nil
}

type releasePeriod struct {
	from, to time.Time
}

func releasePeriods(now time.Time) []releasePeriod {
	year := now.Year()
	decade := year / 10 * 10
	return []releasePeriod{
		{date(decade, 1, 1), now},
		{date(decade-10, 1, 1), date(decade-1, 12, 31)},
		{date(decade-20, 1, 1), date(decade-11, 12, 31)},
		{date(decade-40, 1, 1), date(decade-21, 12, 31)},
		{date(1870, 1, 1), date(decade-41, 12, 31)},
	}
}

func date(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func (s *GenreScenario) genreMovies(ctx context.Context, roundID, genreID int64, limit int, recent, seen map[int64]bool, now time.Time) ([]Movie, error) {
	periods := releasePeriods(now)
	pools, err := s.periodPools(ctx, genreID, periods, limit, recent, seen)
	if err != nil {
		return nil, err
	}
	groups := make([][]Movie, len(periods))
	var reserve []Movie
	for index, pool := range pools {
		quota := periodQuota(limit, len(periods), index)
		shuffleMovies(pool, movieSeed(roundID, genreID, index))
		selected := min(quota, len(pool))
		groups[index] = pool[:selected]
		reserve = append(reserve, pool[selected:]...)
	}
	shuffleMovieGroups(groups, movieSeed(roundID, genreID, len(periods)))
	movies := interleaveMovies(groups, limit)
	for _, movie := range movies {
		seen[movie.ID] = true
	}
	shuffleMovies(reserve, movieSeed(roundID, genreID, len(periods)+1))
	for _, movie := range reserve {
		if len(movies) == limit {
			break
		}
		if !seen[movie.ID] {
			seen[movie.ID] = true
			movies = append(movies, movie)
		}
	}
	return movies, nil
}

func (s *GenreScenario) periodPools(ctx context.Context, genreID int64, periods []releasePeriod, limit int, recent, seen map[int64]bool) ([][]Movie, error) {
	pools := make([][]Movie, len(periods))
	errorsOut := make(chan error, len(periods))
	fetchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var workers sync.WaitGroup
	for index, period := range periods {
		workers.Go(func() {
			blocked := blockedMovieIDs(recent, seen)
			pool, err := s.periodMovies(fetchCtx, genreID, period, periodQuota(limit, len(periods), index), blocked)
			if err != nil {
				errorsOut <- err
				cancel()
				return
			}
			pools[index] = pool
		})
	}
	workers.Wait()
	close(errorsOut)
	if err := firstError(errorsOut); err != nil {
		return nil, err
	}
	blocked := blockedMovieIDs(recent, seen)
	for index := range pools {
		pools[index] = uniqueMovies(pools[index], blocked)
	}
	return pools, nil
}

func firstError(errorsOut <-chan error) error {
	for err := range errorsOut {
		return err
	}
	return nil
}

func uniqueMovies(movies []Movie, blocked map[int64]bool) []Movie {
	unique := movies[:0]
	for _, movie := range movies {
		if blocked[movie.ID] {
			continue
		}
		blocked[movie.ID] = true
		unique = append(unique, movie)
	}
	return unique
}

func (s *GenreScenario) periodMovies(ctx context.Context, genreID int64, period releasePeriod, quota int, blocked map[int64]bool) ([]Movie, error) {
	pool, err := s.discoverPeriod(ctx, genreID, period, 300, blocked)
	if err != nil || len(pool) >= quota {
		return pool, err
	}
	fallback, err := s.discoverPeriod(ctx, genreID, period, 100, blocked)
	return append(pool, fallback...), err
}

func (s *GenreScenario) discoverPeriod(ctx context.Context, genreID int64, period releasePeriod, minVotes int, blocked map[int64]bool) ([]Movie, error) {
	query := DiscoverQuery{GenreID: genreID, Page: 1, MinVotes: minVotes, FromDate: period.from, ToDate: period.to, Sort: DiscoverByRating}
	batch, err := s.Catalog.Discover(ctx, query)
	if err != nil {
		return nil, err
	}
	var movies []Movie
	for _, movie := range batch {
		if movie.Year < period.from.Year() || movie.Year > period.to.Year() || blocked[movie.ID] {
			continue
		}
		blocked[movie.ID] = true
		movies = append(movies, movie)
	}
	return movies, nil
}

func blockedMovieIDs(recent, selected map[int64]bool) map[int64]bool {
	blocked := make(map[int64]bool, len(recent)+len(selected))
	for id := range recent {
		blocked[id] = true
	}
	for id := range selected {
		blocked[id] = true
	}
	return blocked
}

func periodQuota(limit, periods, index int) int {
	quota := limit / periods
	if index < limit%periods {
		quota++
	}
	return quota
}

func movieSeed(roundID, genreID int64, index int) uint64 {
	return uint64(roundID)*0x9e3779b97f4a7c15 ^ uint64(genreID)*0xd1b54a32d192ed03 ^ uint64(index+1)*0x94d049bb133111eb
}

func shuffleMovies(movies []Movie, seed uint64) {
	// #nosec G404 -- deterministic product rotation must survive retries and restarts.
	random := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	random.Shuffle(len(movies), func(i, j int) { movies[i], movies[j] = movies[j], movies[i] })
}

func shuffleMovieGroups(groups [][]Movie, seed uint64) {
	// #nosec G404 -- deterministic product rotation must survive retries and restarts.
	random := rand.New(rand.NewPCG(seed, seed^0xd1b54a32d192ed03))
	random.Shuffle(len(groups), func(i, j int) { groups[i], groups[j] = groups[j], groups[i] })
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

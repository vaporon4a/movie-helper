package movieclub

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"
)

type scenarioCatalog struct {
	mu          sync.Mutex
	calls       []DiscoverQuery
	sparse      bool
	emptyBefore int
	err         error
}

func (c *scenarioCatalog) Discover(_ context.Context, query DiscoverQuery) ([]Movie, error) {
	c.mu.Lock()
	c.calls = append(c.calls, query)
	c.mu.Unlock()
	if c.err != nil {
		return nil, c.err
	}
	if query.ToDate.Year() <= c.emptyBefore {
		return nil, nil
	}
	count := 8
	if c.sparse && query.MinVotes == 300 {
		count = 1
	}
	movies := make([]Movie, count)
	base := query.GenreID*10_000_000 + int64(query.FromDate.Year()*100)
	for i := range movies {
		movies[i] = Movie{
			ID: base + int64(i), Title: fmt.Sprintf("G%d-%d", query.GenreID, i),
			Year: query.FromDate.Year(), Rating: 7.5, VoteCount: query.MinVotes,
		}
	}
	return movies, nil
}

func (*scenarioCatalog) PosterURL(path string) string { return path }

type scenarioHistory map[int64]bool

func (h scenarioHistory) RecentMovieIDs(context.Context, int64, time.Time) (map[int64]bool, error) {
	return h, nil
}

func TestGenreScenarioBalancesReleasePeriodsDeterministically(t *testing.T) {
	catalog := &scenarioCatalog{}
	scenario, err := NewGenreScenario(catalog, scenarioHistory{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	round := Round{ID: 42, ChatID: -1}
	winner := []Option{{ProviderID: 35, Label: "Комедия"}}

	_, first, err := scenario.Recommendations(context.Background(), round, winner, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 20 || len(catalog.calls) != 5 {
		t.Fatalf("movies=%d calls=%d", len(first), len(catalog.calls))
	}
	assertPeriodCounts(t, first, now, []int{4, 4, 4, 4, 4})
	assertPeriodCounts(t, first[:10], now, []int{2, 2, 2, 2, 2})

	catalog.calls = nil
	_, second, err := scenario.Recommendations(context.Background(), round, winner, now)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(movieIDs(first), movieIDs(second)) {
		t.Fatalf("same round changed selection: %v != %v", movieIDs(first), movieIDs(second))
	}
	catalog.calls = nil
	_, other, err := scenario.Recommendations(context.Background(), Round{ID: 43, ChatID: -1}, winner, now)
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(movieIDs(first), movieIDs(other)) {
		t.Fatal("different rounds produced identical ordering")
	}
}

func TestGenreScenarioFallsBackOnlyForSparsePeriods(t *testing.T) {
	catalog := &scenarioCatalog{sparse: true}
	scenario, err := NewGenreScenario(catalog, scenarioHistory{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	_, movies, err := scenario.Recommendations(context.Background(), Round{ID: 10, ChatID: -1}, []Option{{ProviderID: 37}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(movies) != 20 || len(catalog.calls) != 10 {
		t.Fatalf("movies=%d calls=%d", len(movies), len(catalog.calls))
	}
	thresholds := make(map[string][]int)
	for _, call := range catalog.calls {
		key := call.FromDate.Format(time.DateOnly)
		thresholds[key] = append(thresholds[key], call.MinVotes)
	}
	for period, got := range thresholds {
		slices.Sort(got)
		if !slices.Equal(got, []int{100, 300}) {
			t.Fatalf("period %s thresholds=%v", period, got)
		}
	}
	assertUniqueMovies(t, movies)
}

func TestGenreScenarioExcludesRecentMoviesBeforeRotation(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	recent := scenarioHistory{}
	for _, period := range releasePeriods(now) {
		base := int64(35*10_000_000 + period.from.Year()*100)
		for offset := range 4 {
			recent[base+int64(offset)] = true
		}
	}
	catalog := &scenarioCatalog{}
	scenario, err := NewGenreScenario(catalog, recent)
	if err != nil {
		t.Fatal(err)
	}
	_, movies, err := scenario.Recommendations(context.Background(), Round{ID: 12, ChatID: -1}, []Option{{ProviderID: 35}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(movies) != 20 || len(catalog.calls) != 5 {
		t.Fatalf("movies=%d calls=%d", len(movies), len(catalog.calls))
	}
	for _, movie := range movies {
		if recent[movie.ID] {
			t.Fatalf("recent movie selected: %d", movie.ID)
		}
	}
}

func TestGenreScenarioTransfersEmptyPeriodQuotaAndKeepsGenreParity(t *testing.T) {
	catalog := &scenarioCatalog{emptyBefore: 1979}
	scenario, err := NewGenreScenario(catalog, scenarioHistory{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	winners := []Option{{ProviderID: 28}, {ProviderID: 16}}
	_, movies, err := scenario.Recommendations(context.Background(), Round{ID: 11, ChatID: -1}, winners, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(movies) != 20 {
		t.Fatalf("movies=%d", len(movies))
	}
	byGenre := map[string]int{}
	for _, movie := range movies {
		byGenre[movie.Title[:3]]++
		if movie.Year <= 1979 {
			t.Fatalf("empty period leaked movie %#v", movie)
		}
	}
	if byGenre["G28"] != 10 || byGenre["G16"] != 10 {
		t.Fatalf("genre balance=%v", byGenre)
	}
	assertUniqueMovies(t, movies)
}

func TestGenreScenarioFailsWholeSelectionOnCatalogError(t *testing.T) {
	catalogErr := errors.New("tmdb unavailable")
	scenario, err := NewGenreScenario(&scenarioCatalog{err: catalogErr}, scenarioHistory{})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = scenario.Recommendations(context.Background(), Round{ID: 1, ChatID: -1}, []Option{{ProviderID: 35}}, time.Now())
	if !errors.Is(err, catalogErr) {
		t.Fatalf("error=%v", err)
	}
}

func assertPeriodCounts(t *testing.T, movies []Recommendation, now time.Time, want []int) {
	t.Helper()
	periods := releasePeriods(now)
	got := make([]int, len(periods))
	for _, movie := range movies {
		matched := false
		for index, period := range periods {
			if movie.Year >= period.from.Year() && movie.Year <= period.to.Year() {
				got[index]++
				matched = true
				break
			}
		}
		if !matched {
			t.Fatalf("year %d outside periods", movie.Year)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("period counts=%v want=%v", got, want)
	}
}

func assertUniqueMovies(t *testing.T, movies []Recommendation) {
	t.Helper()
	seen := make(map[int64]bool)
	for _, movie := range movies {
		if seen[movie.ID] {
			t.Fatalf("duplicate movie %d", movie.ID)
		}
		seen[movie.ID] = true
	}
}

func movieIDs(movies []Recommendation) []int64 {
	ids := make([]int64, len(movies))
	for i := range movies {
		ids[i] = movies[i].ID
	}
	return ids
}

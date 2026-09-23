package movieclub

import (
	"context"
	"errors"
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
func (*GenreScenario) Options(seed uint64) []Option {
	return GenreOptions(seed)
}
func (*GenreScenario) Winners(options []Option, seed uint64) []Option {
	return Winners(options, seed)
}

func (s *GenreScenario) Recommendations(ctx context.Context, round Round, winners []Option, now time.Time) ([]Recommendation, error) {
	recent, err := s.History.RecentMovieIDs(ctx, round.ChatID, now.Add(-recentWindow))
	if err != nil {
		return nil, err
	}
	groups := make([][]Movie, len(winners))
	seen := make(map[int64]bool)
	for i, winner := range winners {
		groups[i], err = s.genreMovies(ctx, winner.ProviderID, 20/len(winners), recent, seen)
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

func (s *GenreScenario) genreMovies(ctx context.Context, genreID int64, limit int, recent, seen map[int64]bool) ([]Movie, error) {
	var movies []Movie
	for _, minVotes := range []int{300, 100} {
		for page := 1; page <= 2; page++ {
			batch, err := s.Catalog.Discover(ctx, genreID, page, minVotes)
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

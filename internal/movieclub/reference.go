package movieclub

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"time"
)

const referenceHistoryWindow = 8 * 7 * 24 * time.Hour

type ReferenceScenario struct {
	Catalog ReferenceCatalog
	History ReferenceHistory
	Seeds   []ReferenceSeed
}

func NewReferenceScenario(catalog ReferenceCatalog, history ReferenceHistory) (*ReferenceScenario, error) {
	if catalog == nil || history == nil {
		return nil, errors.New("movie reference scenario requires catalog and history")
	}
	return &ReferenceScenario{Catalog: catalog, History: history, Seeds: ReferenceSeeds}, nil
}

func (*ReferenceScenario) Feature() Feature { return Reference }

func (s *ReferenceScenario) Options(ctx context.Context, chatID int64, seed uint64, now time.Time) ([]Option, error) {
	recent, err := s.History.RecentReferenceSeedIDs(ctx, chatID, now.Add(-referenceHistoryWindow))
	if err != nil {
		return nil, err
	}
	candidates := append([]ReferenceSeed(nil), s.Seeds...)
	// #nosec G404 -- poll rotation must be deterministic across retries.
	random := rand.New(rand.NewPCG(seed, seed^0x94d049bb133111eb))
	random.Shuffle(len(candidates), func(i, j int) { candidates[i], candidates[j] = candidates[j], candidates[i] })
	options, usedGroups := make([]Option, 0, 8), make(map[string]bool)
	usedDecades := make(map[int]bool)
	for _, allowRecent := range []bool{false, true} {
		options = s.selectReferenceOptions(ctx, candidates, recent, allowRecent, true, options, usedGroups, usedDecades)
		options = s.selectReferenceOptions(ctx, candidates, recent, allowRecent, false, options, usedGroups, usedDecades)
		if len(options) == 8 {
			return options, nil
		}
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return nil, errors.New("not enough validated reference movies")
}

func (s *ReferenceScenario) selectReferenceOptions(ctx context.Context, candidates []ReferenceSeed, recent map[int64]bool, allowRecent, requireNewDecade bool, options []Option, usedGroups map[string]bool, usedDecades map[int]bool) []Option {
	for _, candidate := range candidates {
		if len(options) == 8 || (requireNewDecade && len(usedDecades) >= 3) {
			break
		}
		if usedGroups[candidate.Group] || (!allowRecent && recent[candidate.ID]) || optionContains(options, candidate.ID) || (requireNewDecade && usedDecades[candidate.Decade]) {
			continue
		}
		option, ok := s.referenceOption(ctx, candidate, len(options))
		if !ok {
			continue
		}
		options = append(options, option)
		usedGroups[candidate.Group] = true
		usedDecades[candidate.Decade] = true
	}
	return options
}

func (s *ReferenceScenario) referenceOption(ctx context.Context, candidate ReferenceSeed, position int) (Option, bool) {
	details, err := s.Catalog.Details(ctx, candidate.ID)
	if err != nil || details.ID != candidate.ID || strings.TrimSpace(details.Title) == "" {
		return Option{}, false
	}
	label := details.Title
	if details.Year != 0 {
		label = fmt.Sprintf("%s (%d)", label, details.Year)
	}
	return Option{ProviderID: candidate.ID, Position: position, Kind: OptionMovie, Label: label}, true
}

func optionContains(options []Option, id int64) bool {
	return slices.ContainsFunc(options, func(option Option) bool { return option.ProviderID == id })
}

func (*ReferenceScenario) Winners(options []Option, seed uint64) []Option {
	winners := Winners(options, seed)
	if len(winners) > 1 {
		return winners[:1]
	}
	return winners
}

func (s *ReferenceScenario) Recommendations(ctx context.Context, round Round, winners []Option, now time.Time) (Movie, []Recommendation, error) {
	if len(winners) != 1 {
		return Movie{}, nil, errors.New("reference poll requires one winner")
	}
	seedID := winners[0].ProviderID
	details, err := s.Catalog.Details(ctx, seedID)
	if err != nil {
		return Movie{}, nil, err
	}
	recent, err := s.History.RecentMovieIDs(ctx, round.ChatID, now.Add(-recentWindow))
	if err != nil {
		return Movie{}, nil, err
	}
	blocked := map[int64]bool{seedID: true}
	for id := range recent {
		blocked[id] = true
	}
	var selected []Recommendation
	recommendations, err := s.Catalog.Recommendations(ctx, seedID)
	if err != nil {
		return Movie{}, nil, err
	}
	similar, err := s.Catalog.Similar(ctx, seedID)
	if err != nil {
		return Movie{}, nil, err
	}
	selected = appendRelation(selected, mergeRelated(recommendations, similar), "similar", 5, round.ID, blocked)
	selected, err = s.appendCrewRelation(ctx, selected, details.Crew, []string{"Director"}, "director", 2, round.ID, blocked)
	if err != nil {
		return Movie{}, nil, err
	}
	selected, err = s.appendCrewRelation(ctx, selected, details.Crew, []string{"Screenplay", "Writer", "Story"}, "screenwriter", 2, round.ID, blocked)
	if err != nil {
		return Movie{}, nil, err
	}
	selected, err = s.appendCrewRelation(ctx, selected, details.Crew, []string{"Novel", "Book"}, "book_author", 1, round.ID, blocked)
	if err != nil {
		return Movie{}, nil, err
	}
	for i := range selected {
		selected[i].Page = 1
		selected[i].Position = i
	}
	return details.Movie, selected, nil
}

func (s *ReferenceScenario) appendCrewRelation(ctx context.Context, out []Recommendation, crew []Credit, jobs []string, relation string, limit int, roundID int64, blocked map[int64]bool) ([]Recommendation, error) {
	var candidates []Movie
	seenPeople := make(map[int64]bool)
	for _, credit := range crew {
		if seenPeople[credit.PersonID] || !slices.Contains(jobs, credit.Job) {
			continue
		}
		seenPeople[credit.PersonID] = true
		movies, err := s.Catalog.PersonMovies(ctx, credit.PersonID)
		if err != nil {
			return nil, err
		}
		for _, movie := range movies {
			if slices.Contains(jobs, movie.Job) {
				candidates = append(candidates, movie.Movie)
			}
		}
		if len(seenPeople) == 2 {
			break
		}
	}
	sortMovies(candidates)
	return appendRelation(out, candidates, relation, limit, roundID, blocked), nil
}

func mergeRelated(first, second []Movie) []Movie {
	counts := make(map[int64]int)
	byID := make(map[int64]Movie)
	for _, list := range [][]Movie{first, second} {
		for _, movie := range list {
			counts[movie.ID]++
			byID[movie.ID] = movie
		}
	}
	out := make([]Movie, 0, len(byID))
	for _, movie := range byID {
		out = append(out, movie)
	}
	slices.SortStableFunc(out, func(a, b Movie) int {
		if counts[a.ID] != counts[b.ID] {
			return counts[b.ID] - counts[a.ID]
		}
		if a.VoteCount != b.VoteCount {
			return b.VoteCount - a.VoteCount
		}
		if a.Popularity > b.Popularity {
			return -1
		}
		if a.Popularity < b.Popularity {
			return 1
		}
		return int(a.ID - b.ID)
	})
	return out
}

func sortMovies(movies []Movie) {
	slices.SortStableFunc(movies, func(a, b Movie) int {
		if a.VoteCount != b.VoteCount {
			return b.VoteCount - a.VoteCount
		}
		if a.Popularity > b.Popularity {
			return -1
		}
		if a.Popularity < b.Popularity {
			return 1
		}
		return int(a.ID - b.ID)
	})
}

func appendRelation(out []Recommendation, candidates []Movie, relation string, limit int, roundID int64, blocked map[int64]bool) []Recommendation {
	added := 0
	for _, movie := range candidates {
		if added == limit {
			break
		}
		if movie.ID <= 0 || strings.TrimSpace(movie.Title) == "" || blocked[movie.ID] {
			continue
		}
		blocked[movie.ID] = true
		out = append(out, Recommendation{Movie: movie, RoundID: roundID, Relation: relation})
		added++
	}
	return out
}

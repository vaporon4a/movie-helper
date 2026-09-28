package movieclub

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
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
	candidates, settings := s.referenceCandidates(ctx, chatID, seed, now)
	options, err := s.selectEightReferenceOptions(ctx, candidates, recent)
	if err != nil {
		return nil, err
	}
	decorateReferenceOptions(options, settings)
	return options, nil
}

func (s *ReferenceScenario) referenceCandidates(ctx context.Context, chatID int64, seed uint64, now time.Time) ([]ReferenceSeed, PreferenceSettings) {
	candidates := append([]ReferenceSeed(nil), s.Seeds...)
	// #nosec G404 -- poll rotation must be deterministic across retries.
	random := rand.New(rand.NewPCG(seed, seed^0x94d049bb133111eb))
	random.Shuffle(len(candidates), func(i, j int) { candidates[i], candidates[j] = candidates[j], candidates[i] })
	settings, profile, _, tasteErr := loadTaste(ctx, s.History, nil, chatID, 0, now)
	if tasteErr != nil {
		slog.WarnContext(ctx, "movieclub taste fallback", "chat_id", chatID, "feature", Reference, "operation", "options", "reason", "profile_unavailable")
		settings = PreferenceSettings{ChatID: chatID, Mode: PersonalizationOff, PolicyVersion: LegacyPolicyVersion}
		profile = ProjectTaste(nil, now)
	}
	if settings.Mode == PersonalizationOff || profile.CompletedRounds == 0 {
		return candidates, settings
	}
	adaptive := adaptiveReferenceSeeds(candidates, profile)
	if settings.Mode == PersonalizationOn {
		return adaptive, settings
	}
	slog.InfoContext(ctx, "movieclub reference option shadow", "chat_id", chatID, "policy_version", settings.PolicyVersion,
		"seed_overlap_at_8", seedOverlap(candidates, adaptive, 8))
	return candidates, settings
}

func (s *ReferenceScenario) selectEightReferenceOptions(ctx context.Context, candidates []ReferenceSeed, recent map[int64]bool) ([]Option, error) {
	options, usedGroups := make([]Option, 0, 8), make(map[string]bool)
	usedDecades := make(map[int]bool)
	for _, allowRecent := range []bool{false, true} {
		options = s.selectReferenceOptions(ctx, candidates, recent, allowRecent, true, options, usedGroups, usedDecades)
		options = s.selectReferenceOptions(ctx, candidates, recent, allowRecent, false, options, usedGroups, usedDecades)
		if len(options) == 8 {
			return options, nil
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, errors.New("not enough validated reference movies")
}

func decorateReferenceOptions(options []Option, settings PreferenceSettings) {
	for index := range options {
		options[index].PolicyVersion = LegacyPolicyVersion
		options[index].SelectionRole = SelectionLegacy
		if settings.Mode != PersonalizationOn {
			continue
		}
		options[index].PolicyVersion = settings.PolicyVersion
		switch {
		case index < 5:
			options[index].SelectionRole = SelectionExploit
		case index < 7:
			options[index].SelectionRole = SelectionExplore
		default:
			options[index].SelectionRole = SelectionWildcard
		}
	}
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
	return Option{ProviderID: candidate.ID, Position: position, Kind: OptionMovie, Label: label,
		Metadata: MovieMetadata{GenreIDs: details.Genres, ReleaseYear: details.Year}}, true
}

func adaptiveReferenceSeeds(candidates []ReferenceSeed, profile ChatTasteProfile) []ReferenceSeed {
	out := append([]ReferenceSeed(nil), candidates...)
	slices.SortStableFunc(out, func(a, b ReferenceSeed) int {
		aScore := referenceSeedScore(a, profile)
		bScore := referenceSeedScore(b, profile)
		if aScore > bScore {
			return -1
		}
		if aScore < bScore {
			return 1
		}
		return 0
	})
	return out
}

func referenceSeedScore(seed ReferenceSeed, profile ChatTasteProfile) float64 {
	genreAffinity := 0.0
	genreExposure := 0
	for _, genreID := range referenceSeedGenres(seed.Group) {
		genreAffinity = math.Max(genreAffinity, profile.GenreAffinity[genreID])
		genreExposure += profile.GenreExposure[genreID]
	}
	affinity := 0.75*genreAffinity + 0.25*profile.DecadeAffinity[seed.Decade]
	exploration := 1 / math.Sqrt(1+float64(genreExposure+profile.DecadeExposure[seed.Decade]))
	return affinity + 0.25*exploration
}

func seedOverlap(first, second []ReferenceSeed, limit int) int {
	seen := make(map[int64]bool)
	for _, seed := range first[:min(limit, len(first))] {
		seen[seed.ID] = true
	}
	result := 0
	for _, seed := range second[:min(limit, len(second))] {
		if seen[seed.ID] {
			result++
		}
	}
	return result
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

func (s *ReferenceScenario) Recommendations(ctx context.Context, round Round, winners []Option, now time.Time) (Selection, error) {
	if len(winners) != 1 {
		return Selection{}, errors.New("reference poll requires one winner")
	}
	seedID := winners[0].ProviderID
	details, err := s.Catalog.Details(ctx, seedID)
	if err != nil {
		return Selection{}, err
	}
	recent, err := s.History.RecentMovieIDs(ctx, round.ChatID, now.Add(-recentWindow))
	if err != nil {
		return Selection{}, err
	}
	settings, profile, exposures, err := loadTaste(ctx, s.History, s.Catalog, round.ChatID, round.ID, now)
	if err != nil {
		slog.WarnContext(ctx, "movieclub taste fallback", "round_id", round.ID, "chat_id", round.ChatID, "feature", Reference,
			"operation", "recommendations", "reason", "profile_unavailable")
		settings = PreferenceSettings{ChatID: round.ChatID, Mode: PersonalizationOff, PolicyVersion: LegacyPolicyVersion}
		profile = ProjectTaste(nil, now)
		exposures = nil
	}
	baseBlocked := map[int64]bool{seedID: true}
	for id := range recent {
		baseBlocked[id] = true
	}
	pools, err := s.referenceRecommendationPools(ctx, seedID, details.Crew)
	if err != nil {
		return Selection{}, err
	}
	legacyBlocked, adaptiveBlocked := cloneBlocked(baseBlocked), cloneBlocked(baseBlocked)
	var legacy, adaptive []Recommendation
	var candidates []RankingCandidate
	for _, spec := range []struct {
		relation string
		limit    int
	}{{"similar", 5}, {"director", 2}, {"screenwriter", 2}, {"book_author", 1}} {
		legacyPool := eligibleMovies(pools[spec.relation], legacyBlocked)
		adaptivePool := eligibleMovies(pools[spec.relation], adaptiveBlocked)
		legacy = appendRelation(legacy, legacyPool, spec.relation, spec.limit, round.ID, legacyBlocked)
		ranked := RankMovies(adaptivePool, profile, exposures, nil, DefaultRankingPolicy)
		if profile.CompletedRounds == 0 {
			ranked = RankMoviesInCurrentOrder(adaptivePool, profile, exposures, DefaultRankingPolicy)
		}
		adaptive = appendRelation(adaptive, rankedMovieValues(ranked), spec.relation, spec.limit, round.ID, adaptiveBlocked)
		candidates = append(candidates, rankingCandidates(round.ID, spec.relation, legacyPool, ranked)...)
	}
	selected := legacy
	if settings.Mode == PersonalizationOn {
		selected = adaptive
	}
	for i := range selected {
		selected[i].Page = 1
		selected[i].Position = i
		ranked := findRanked(selected[i].ID, candidates)
		selected[i].RankingScore = ranked.RankingScore
		selected[i].Ranking = ranked.Ranking
		selected[i].PolicyVersion = settings.PolicyVersion
	}
	markCandidateSelections(candidates, legacy, adaptive, settings.Mode)
	if settings.Mode != PersonalizationOff {
		slog.InfoContext(ctx, "movieclub reference ranking compared", "round_id", round.ID, "chat_id", round.ChatID,
			"mode", settings.Mode, "policy_version", settings.PolicyVersion,
			"overlap_at_10", overlapAt(recommendationMovies(legacy), recommendationMovies(adaptive), 10))
	}
	return Selection{Hero: details.Movie, Movies: selected, Candidates: candidates, Mode: settings.Mode, PolicyVersion: settings.PolicyVersion}, nil
}

func (s *ReferenceScenario) referenceRecommendationPools(ctx context.Context, seedID int64, crew []Credit) (map[string][]Movie, error) {
	recommendations, err := s.Catalog.Recommendations(ctx, seedID)
	if err != nil {
		return nil, err
	}
	similar, err := s.Catalog.Similar(ctx, seedID)
	if err != nil {
		return nil, err
	}
	pools := map[string][]Movie{"similar": mergeRelated(recommendations, similar)}
	for _, spec := range []struct {
		relation string
		jobs     []string
	}{{"director", []string{"Director"}}, {"screenwriter", []string{"Screenplay", "Writer", "Story"}}, {"book_author", []string{"Novel", "Book"}}} {
		pools[spec.relation], err = s.crewMovies(ctx, crew, spec.jobs)
		if err != nil {
			return nil, err
		}
	}
	return pools, nil
}

func (s *ReferenceScenario) crewMovies(ctx context.Context, crew []Credit, jobs []string) ([]Movie, error) {
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
	return candidates, nil
}

func cloneBlocked(source map[int64]bool) map[int64]bool {
	out := make(map[int64]bool, len(source))
	for id := range source {
		out[id] = true
	}
	return out
}

func eligibleMovies(source []Movie, blocked map[int64]bool) []Movie {
	out := make([]Movie, 0, len(source))
	seen := make(map[int64]bool, len(source))
	for _, movie := range source {
		if movie.ID <= 0 || strings.TrimSpace(movie.Title) == "" || blocked[movie.ID] || seen[movie.ID] {
			continue
		}
		seen[movie.ID] = true
		out = append(out, movie)
	}
	return out
}

func recommendationMovies(values []Recommendation) []Movie {
	out := make([]Movie, len(values))
	for index := range values {
		out[index] = values[index].Movie
	}
	return out
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

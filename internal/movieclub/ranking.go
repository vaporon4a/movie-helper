package movieclub

import (
	"math"
	"slices"
)

type RankingPolicy struct {
	Version                                                    string
	Quality, Affinity, Novelty, Exploration, Source, MMRLambda float64
}

var DefaultRankingPolicy = RankingPolicy{
	Version: RankingPolicyV1, Quality: 0.25, Affinity: 0.15,
	Novelty: 0.15, Exploration: 0.10, Source: 0.35, MMRLambda: 0.8,
}

type RankedMovie struct {
	Movie
	Score     float64
	Breakdown RankingBreakdown
}

func RankMovies(movies []Movie, profile ChatTasteProfile, exposures map[int64]int, relevance map[int64]float64, policy RankingPolicy) []RankedMovie {
	pool := make([]RankedMovie, 0, len(movies))
	for _, movie := range movies {
		if movie.ID <= 0 {
			continue
		}
		breakdown := rankingBreakdown(movie, profile, exposures[movie.ID], relevance[movie.ID])
		score := policy.Source*breakdown.SourceRelevance + policy.Quality*breakdown.Quality +
			policy.Novelty*breakdown.Novelty + policy.Exploration*breakdown.Exploration +
			policy.Affinity*profileWeight(profile.EffectiveVotes)*breakdown.Affinity
		pool = append(pool, RankedMovie{Movie: movie, Score: score, Breakdown: breakdown})
	}
	slices.SortStableFunc(pool, compareRankedMovies)
	return mmrOrder(pool, policy.MMRLambda)
}

func RankMoviesInCurrentOrder(movies []Movie, profile ChatTasteProfile, exposures map[int64]int, policy RankingPolicy) []RankedMovie {
	ranked := RankMovies(movies, profile, exposures, nil, policy)
	byID := make(map[int64]RankedMovie, len(ranked))
	for _, movie := range ranked {
		byID[movie.ID] = movie
	}
	out := make([]RankedMovie, 0, len(movies))
	for _, movie := range movies {
		if value, ok := byID[movie.ID]; ok {
			out = append(out, value)
		}
	}
	return out
}

func rankingBreakdown(movie Movie, profile ChatTasteProfile, exposure int, relevance float64) RankingBreakdown {
	quality := (float64(movie.VoteCount)/(float64(movie.VoteCount)+300))*(clamp(movie.Rating/10)) +
		(300/(float64(movie.VoteCount)+300))*0.65
	if relevance <= 0 {
		relevance = 1
	}
	genreAffinity, genreExposure := 0.0, 0.0
	genres := validGenreIDs(movie.Genres)
	for _, genreID := range genres {
		genreAffinity = math.Max(genreAffinity, profile.GenreAffinity[genreID])
		genreExposure += float64(profile.GenreExposure[genreID])
	}
	if len(genres) > 0 {
		genreExposure /= float64(len(genres))
	}
	decade := releaseDecade(movie.Year)
	affinity := clamp(0.8*genreAffinity + 0.2*profile.DecadeAffinity[decade])
	exploration := 1 / math.Sqrt(1+genreExposure+float64(profile.DecadeExposure[decade]))
	return RankingBreakdown{
		Quality: clamp(quality), Affinity: affinity, Novelty: 1 / float64(1+max(0, exposure)),
		Exploration: clamp(exploration), SourceRelevance: clamp(relevance),
	}
}

func compareRankedMovies(a, b RankedMovie) int {
	if a.Score != b.Score {
		if a.Score > b.Score {
			return -1
		}
		return 1
	}
	if a.VoteCount != b.VoteCount {
		return b.VoteCount - a.VoteCount
	}
	if a.ID < b.ID {
		return -1
	}
	if a.ID > b.ID {
		return 1
	}
	return 0
}

func mmrOrder(pool []RankedMovie, lambda float64) []RankedMovie {
	remaining := append([]RankedMovie(nil), pool...)
	selected := make([]RankedMovie, 0, len(pool))
	for len(remaining) > 0 {
		best, bestValue := 0, math.Inf(-1)
		for index, candidate := range remaining {
			similarity := 0.0
			for _, chosen := range selected {
				similarity = math.Max(similarity, genreJaccard(candidate.Genres, chosen.Genres))
			}
			value := lambda*candidate.Score - (1-lambda)*similarity
			if value > bestValue || (value == bestValue && compareRankedMovies(candidate, remaining[best]) < 0) {
				best, bestValue = index, value
			}
		}
		selected = append(selected, remaining[best])
		remaining = append(remaining[:best], remaining[best+1:]...)
	}
	return selected
}

func genreJaccard(first, second []int64) float64 {
	a, b := validGenreIDs(first), validGenreIDs(second)
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	intersection := 0
	for _, value := range a {
		if slices.Contains(b, value) {
			intersection++
		}
	}
	return float64(intersection) / float64(len(a)+len(b)-intersection)
}

func clamp(value float64) float64 { return math.Max(0, math.Min(1, value)) }

package movieclub

import (
	"math"
	"reflect"
	"testing"
)

func TestRankMoviesIsDeterministicAndUsesTasteWithoutDroppingCandidates(t *testing.T) {
	movies := []Movie{
		{ID: 1, Title: "Comedy", Year: 2001, VoteCount: 500, Rating: 7.2, Genres: []int64{35}},
		{ID: 2, Title: "Drama", Year: 2002, VoteCount: 500, Rating: 7.8, Genres: []int64{18}},
		{ID: 3, Title: "Mixed", Year: 2003, VoteCount: 400, Rating: 7.5, Genres: []int64{35, 18}},
	}
	profile := ChatTasteProfile{
		GenreAffinity: map[int64]float64{35: 1, 18: 0.1}, DecadeAffinity: map[int]float64{2000: 1},
		GenreExposure: map[int64]int{}, DecadeExposure: map[int]int{}, EffectiveVotes: 100,
	}
	first := RankMovies(movies, profile, map[int64]int{2: 2}, nil, DefaultRankingPolicy)
	second := RankMovies(movies, profile, map[int64]int{2: 2}, nil, DefaultRankingPolicy)
	if !reflect.DeepEqual(first, second) || len(first) != len(movies) {
		t.Fatalf("ranking unstable: %#v %#v", first, second)
	}
	seen := map[int64]bool{}
	for _, movie := range first {
		if seen[movie.ID] || math.IsNaN(movie.Score) || math.IsInf(movie.Score, 0) {
			t.Fatalf("invalid ranked movie %#v", movie)
		}
		seen[movie.ID] = true
	}
	if first[0].ID == 2 {
		t.Fatalf("taste and novelty did not affect ranking: %#v", first)
	}
}

func TestGenreJaccard(t *testing.T) {
	if got := genreJaccard([]int64{35, 18}, []int64{18, 27}); got != 1.0/3.0 {
		t.Fatalf("jaccard=%v", got)
	}
}

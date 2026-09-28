package movieclub

import (
	"reflect"
	"testing"
	"time"
)

func TestProjectTasteUsesAllPositiveVotesAndIgnoresAbstention(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	profile := ProjectTaste([]TasteRound{{ID: 1, ClosedAt: now.Unix(), Feature: Genre, Options: []Option{
		{Kind: OptionGenre, ProviderID: 35, Votes: 6},
		{Kind: OptionGenre, ProviderID: 18, Votes: 4},
		{Kind: OptionGenre, ProviderID: 27, Votes: 0},
	}}}, now)
	if profile.CompletedRounds != 1 || profile.EffectiveVotes != 10 {
		t.Fatalf("profile=%#v", profile)
	}
	if profile.GenreAffinity[35] != 1 || profile.GenreAffinity[18] <= 0 || profile.GenreAffinity[27] != 0 {
		t.Fatalf("genre affinity=%v", profile.GenreAffinity)
	}
	if profile.GenreExposure[35] != 1 || profile.GenreExposure[27] != 1 {
		t.Fatalf("genre exposure=%v", profile.GenreExposure)
	}
}

func TestProjectTasteDecaysOlderRoundsAndUsesReferenceMetadata(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	options := func(genre int64, year int) []Option {
		return []Option{
			{Kind: OptionMovie, Votes: 5, Metadata: MovieMetadata{GenreIDs: []int64{genre}, ReleaseYear: year}},
			{Kind: OptionMovie, Votes: 0, Metadata: MovieMetadata{GenreIDs: []int64{18}, ReleaseYear: 2001}},
		}
	}
	profile := ProjectTaste([]TasteRound{
		{ID: 1, ClosedAt: now.Add(-240 * 24 * time.Hour).Unix(), Feature: Reference, Options: options(35, 1994)},
		{ID: 2, ClosedAt: now.Unix(), Feature: Reference, Options: options(27, 2014)},
	}, now)
	if profile.GenreAffinity[27] <= profile.GenreAffinity[35] || profile.DecadeAffinity[2010] <= profile.DecadeAffinity[1990] {
		t.Fatalf("decay was not applied: genres=%v decades=%v", profile.GenreAffinity, profile.DecadeAffinity)
	}
}

func TestAdaptiveGenreOptionsAreDeterministicAndKeepExploration(t *testing.T) {
	profile := ChatTasteProfile{GenreAffinity: map[int64]float64{35: 1, 18: 0.8, 27: 0.7}, GenreExposure: map[int64]int{35: 10, 18: 8, 27: 7}}
	first := AdaptiveGenreOptions(42, profile)
	second := AdaptiveGenreOptions(42, profile)
	if !reflect.DeepEqual(first, second) || len(first) != 10 {
		t.Fatalf("options are unstable: %#v %#v", first, second)
	}
	roles, ids := map[SelectionRole]int{}, map[int64]bool{}
	for index, option := range first {
		roles[option.SelectionRole]++
		if ids[option.ProviderID] || option.Position != index || option.PolicyVersion != RankingPolicyV1 {
			t.Fatalf("invalid option %#v", option)
		}
		ids[option.ProviderID] = true
	}
	if roles[SelectionExplore] < 3 || roles[SelectionWildcard] != 1 {
		t.Fatalf("roles=%v", roles)
	}
}

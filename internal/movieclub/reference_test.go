package movieclub

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"
)

type referenceCatalogStub struct {
	details map[int64]MovieDetails
	recs    []Movie
	similar []Movie
	people  map[int64][]PersonMovie
}

func (c referenceCatalogStub) Details(_ context.Context, id int64) (MovieDetails, error) {
	details, ok := c.details[id]
	if !ok {
		return MovieDetails{}, fmt.Errorf("missing movie %d", id)
	}
	return details, nil
}
func (c referenceCatalogStub) Recommendations(context.Context, int64) ([]Movie, error) {
	return c.recs, nil
}
func (c referenceCatalogStub) Similar(context.Context, int64) ([]Movie, error) {
	return c.similar, nil
}
func (c referenceCatalogStub) PersonMovies(_ context.Context, id int64) ([]PersonMovie, error) {
	return c.people[id], nil
}

type referenceHistoryStub struct {
	recentMovies map[int64]bool
	recentSeeds  map[int64]bool
}

func (h referenceHistoryStub) RecentMovieIDs(context.Context, int64, time.Time) (map[int64]bool, error) {
	return h.recentMovies, nil
}
func (h referenceHistoryStub) RecentReferenceSeedIDs(context.Context, int64, time.Time) (map[int64]bool, error) {
	return h.recentSeeds, nil
}

func TestReferenceSeedsAreCuratedAndDiverse(t *testing.T) {
	if len(ReferenceSeeds) < 40 || len(ReferenceSeeds) > 60 {
		t.Fatalf("seed count=%d", len(ReferenceSeeds))
	}
	ids, groups, decades := map[int64]bool{}, map[string]bool{}, map[int]bool{}
	for _, seed := range ReferenceSeeds {
		if seed.ID <= 0 || seed.Title == "" || seed.Group == "" || seed.Decade < 1900 || ids[seed.ID] {
			t.Fatalf("invalid seed %#v", seed)
		}
		ids[seed.ID], groups[seed.Group], decades[seed.Decade] = true, true, true
	}
	if len(groups) < 8 || len(decades) < 6 {
		t.Fatalf("groups=%d decades=%d", len(groups), len(decades))
	}
}

func TestReferenceOptionsAreStableDiverseAndAvoidRecentSeeds(t *testing.T) {
	seeds := []ReferenceSeed{
		{1, "A", "g1", 1950}, {2, "B", "g2", 1960}, {3, "C", "g3", 1970}, {4, "D", "g4", 1980},
		{5, "E", "g5", 1990}, {6, "F", "g6", 2000}, {7, "G", "g7", 2010}, {8, "H", "g8", 2020},
		{9, "I", "g9", 1990},
	}
	details := make(map[int64]MovieDetails)
	for _, seed := range seeds {
		details[seed.ID] = MovieDetails{Movie: Movie{ID: seed.ID, Title: seed.Title, Year: seed.Decade + 1}}
	}
	scenario := &ReferenceScenario{Catalog: referenceCatalogStub{details: details}, History: referenceHistoryStub{recentSeeds: map[int64]bool{1: true}}, Seeds: seeds}
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	first, err := scenario.Options(context.Background(), -1, 42, now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := scenario.Options(context.Background(), -1, 42, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 8 || !reflect.DeepEqual(first, second) {
		t.Fatalf("options=%#v second=%#v", first, second)
	}
	seen := make(map[int64]bool)
	for index, option := range first {
		if option.Position != index || option.Kind != OptionMovie || seen[option.ProviderID] || option.ProviderID == 1 {
			t.Fatalf("invalid option %#v", option)
		}
		seen[option.ProviderID] = true
	}
}

func TestReferenceRecommendationsGroupAndDeduplicateResults(t *testing.T) {
	movie := func(id int64, votes int) Movie {
		return Movie{ID: id, Title: fmt.Sprintf("Movie %d", id), Year: 2000 + int(id%20), VoteCount: votes, Rating: 7.5, PosterPath: fmt.Sprintf("/%d.jpg", id)}
	}
	seed := movie(100, 1000)
	catalog := referenceCatalogStub{
		details: map[int64]MovieDetails{100: {Movie: seed, Crew: []Credit{
			{PersonID: 1, Job: "Director"}, {PersonID: 2, Job: "Screenplay"}, {PersonID: 3, Job: "Novel"}, {PersonID: 4, Job: "Characters"},
		}}},
		recs:    []Movie{movie(10, 900), movie(11, 800), movie(12, 700), movie(13, 600), movie(14, 500)},
		similar: []Movie{movie(10, 900), movie(15, 950), movie(16, 400)},
		people: map[int64][]PersonMovie{
			1: {{Movie: movie(20, 800), Job: "Director"}, {Movie: movie(10, 900), Job: "Director"}, {Movie: movie(21, 700), Job: "Director"}},
			2: {{Movie: movie(30, 800), Job: "Screenplay"}, {Movie: movie(31, 700), Job: "Writer"}},
			3: {{Movie: movie(40, 800), Job: "Novel"}, {Movie: movie(41, 700), Job: "Characters"}},
			4: {{Movie: movie(42, 900), Job: "Characters"}},
		},
	}
	scenario := &ReferenceScenario{Catalog: catalog, History: referenceHistoryStub{recentMovies: map[int64]bool{15: true}}}
	hero, got, err := scenario.Recommendations(context.Background(), Round{ID: 7, ChatID: -1}, []Option{{ProviderID: 100}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if hero.ID != 100 || len(got) != 10 {
		t.Fatalf("hero=%#v recommendations=%#v", hero, got)
	}
	wantRelations := []string{"similar", "similar", "similar", "similar", "similar", "director", "director", "screenwriter", "screenwriter", "book_author"}
	seen := map[int64]bool{100: true, 15: true}
	for index, recommendation := range got {
		if recommendation.Relation != wantRelations[index] || recommendation.Page != 1 || recommendation.Position != index || seen[recommendation.ID] {
			t.Fatalf("recommendation %d=%#v", index, recommendation)
		}
		seen[recommendation.ID] = true
	}
}

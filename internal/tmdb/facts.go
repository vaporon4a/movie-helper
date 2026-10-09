package tmdb

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/url"
	"strconv"
	"time"

	"github.com/vaporon4a/movie-helper/internal/facts"
)

type FactExternalIDs = facts.ExternalIDs

func (c *Client) FactExternalIDs(ctx context.Context, id int64) (FactExternalIDs, error) {
	if id <= 0 {
		return FactExternalIDs{}, errors.New("invalid movie id")
	}
	var row struct {
		Wikidata string `json:"wikidata_id"`
		IMDb     string `json:"imdb_id"`
	}
	err := c.get(ctx, fmt.Sprintf("/movie/%d/external_ids", id), &row)
	return FactExternalIDs{Wikidata: row.Wikidata, IMDb: row.IMDb}, err
}

// FactMovies deliberately uses popularity and era diversity, independent of poll discovery.
func (c *Client) FactMovies(ctx context.Context, now time.Time) ([]facts.Movie, error) {
	groups := [][2]int{{1960, 1979}, {1980, 1999}, {2000, 2009}, {2010, 2019}, {2020, now.Year()}, {1950, 2019}, {0, 0}}
	batches := make([][]facts.Movie, 0, len(groups))
	used := map[int64]bool{}
	for n, years := range groups {
		rows, err := c.factGroup(ctx, now, n, years)
		if err != nil {
			return nil, err
		}
		batches = append(batches, pickFactGroup(rows, used))
	}
	var out []facts.Movie
	// Interleave epochs/languages so the worker's first batch is already diverse.
	for n := 0; n < 10; n++ {
		for _, batch := range batches {
			if n < len(batch) {
				out = append(out, batch[n])
			}
		}
	}
	return out, nil
}
func (c *Client) factGroup(ctx context.Context, now time.Time, group int, years [2]int) ([]factMovieResult, error) {
	q := url.Values{"language": {"ru-RU"}, "include_adult": {"false"}, "include_video": {"false"}, "sort_by": {"popularity.desc"}, "vote_count.gte": {"200"}, "primary_release_date.lte": {now.Format(time.DateOnly)}}
	// #nosec G404 -- selecting a catalog page is not security-sensitive.
	q.Set("page", strconv.Itoa(1+rand.IntN(3)))
	path := "/discover/movie"
	if group == 6 {
		path = "/trending/movie/week"
	} else {
		q.Set("primary_release_date.gte", fmt.Sprintf("%04d-01-01", years[0]))
		if years[1] < now.Year() {
			q.Set("primary_release_date.lte", fmt.Sprintf("%04d-12-31", years[1]))
		}
		if group == 5 {
			q.Set("with_original_language", "ru")
			q.Set("vote_count.gte", "50")
		}
	}
	var row struct {
		Results []factMovieResult `json:"results"`
	}
	if err := c.get(ctx, path+"?"+q.Encode(), &row); err != nil {
		return nil, err
	}
	valid := make([]factMovieResult, 0, len(row.Results))
	for _, m := range row.Results {
		date, err := time.Parse(time.DateOnly, m.ReleaseDate)
		if err == nil && !date.After(now) && !m.Adult && !m.Video && m.ID > 0 && m.Title != "" {
			valid = append(valid, m)
		}
	}
	return valid, nil
}

type factMovieResult struct {
	movieResult
	OriginalTitle string `json:"original_title"`
	Video         bool   `json:"video"`
}

func pickFactGroup(rows []factMovieResult, used map[int64]bool) []facts.Movie {
	// #nosec G404 -- diversity does not require cryptographic randomness.
	rand.Shuffle(len(rows), func(i, j int) { rows[i], rows[j] = rows[j], rows[i] })
	out := make([]facts.Movie, 0, 10)
	genreCounts := map[int64]int{}
	for _, capPerGenre := range []int{3, 10} {
		for _, m := range rows {
			genre := int64(0)
			if len(m.GenreIDs) > 0 {
				genre = m.GenreIDs[0]
			}
			if used[m.ID] || genreCounts[genre] >= capPerGenre {
				continue
			}
			year, _ := strconv.Atoi(m.ReleaseDate[:4])
			out = append(out, facts.Movie{ID: m.ID, Title: m.Title, OriginalTitle: m.OriginalTitle, Year: year})
			used[m.ID] = true
			genreCounts[genre]++
			if len(out) == 10 {
				return out
			}
		}
	}
	return out
}

// Package tmdb provides the small, validated subset of TMDB used by movie polls.
package tmdb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/vaporon4a/movie-helper/internal/movieclub"
)

const maxResponseBytes = 2 << 20

type HTTPError struct{ Status int }

func (e *HTTPError) Error() string { return fmt.Sprintf("tmdb http %d", e.Status) }

type Client struct {
	HTTP    *http.Client
	BaseURL string
	Token   string
	Log     *slog.Logger
	mu      sync.Mutex
	image   string
}

type movieResult struct {
	ID          int64   `json:"id"`
	Title       string  `json:"title"`
	Overview    string  `json:"overview"`
	PosterPath  string  `json:"poster_path"`
	ReleaseDate string  `json:"release_date"`
	VoteAverage float64 `json:"vote_average"`
	VoteCount   int     `json:"vote_count"`
	Popularity  float64 `json:"popularity"`
	Adult       bool    `json:"adult"`
}

type creditResult struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Job        string `json:"job"`
	Department string `json:"department"`
}

func (c *Client) Discover(ctx context.Context, query movieclub.DiscoverQuery) ([]movieclub.Movie, error) {
	if query.GenreID <= 0 || query.Page < 1 || query.Page > 500 || query.MinVotes < 0 ||
		query.FromDate.Year() < 1870 || query.ToDate.Year() > 3000 || query.ToDate.Before(query.FromDate) ||
		query.Sort != movieclub.DiscoverByRating {
		return nil, errors.New("invalid discover parameters")
	}
	q := url.Values{
		"language":                 {"ru-RU"},
		"include_adult":            {"false"},
		"include_video":            {"false"},
		"sort_by":                  {string(query.Sort)},
		"vote_average.gte":         {"6"},
		"vote_count.gte":           {strconv.Itoa(query.MinVotes)},
		"with_genres":              {strconv.FormatInt(query.GenreID, 10)},
		"primary_release_date.gte": {query.FromDate.Format(time.DateOnly)},
		"primary_release_date.lte": {query.ToDate.Format(time.DateOnly)},
		"page":                     {strconv.Itoa(query.Page)},
	}
	var payload struct {
		Results []movieResult `json:"results"`
	}
	if err := c.get(ctx, "/discover/movie?"+q.Encode(), &payload); err != nil {
		return nil, err
	}
	return moviesFromResults(payload.Results), nil
}

func (c *Client) Details(ctx context.Context, id int64) (movieclub.MovieDetails, error) {
	if id <= 0 {
		return movieclub.MovieDetails{}, errors.New("invalid movie id")
	}
	var payload struct {
		movieResult
		Genres []struct {
			ID int64 `json:"id"`
		} `json:"genres"`
		Credits struct {
			Crew []creditResult `json:"crew"`
		} `json:"credits"`
	}
	path := fmt.Sprintf("/movie/%d?language=ru-RU&append_to_response=credits", id)
	if err := c.get(ctx, path, &payload); err != nil {
		return movieclub.MovieDetails{}, err
	}
	movies := moviesFromResults([]movieResult{payload.movieResult})
	if len(movies) != 1 {
		return movieclub.MovieDetails{}, errors.New("invalid tmdb movie details")
	}
	details := movieclub.MovieDetails{Movie: movies[0]}
	for _, genre := range payload.Genres {
		if genre.ID > 0 {
			details.Genres = append(details.Genres, genre.ID)
		}
	}
	for _, credit := range payload.Credits.Crew {
		if credit.ID > 0 && strings.TrimSpace(credit.Name) != "" && strings.TrimSpace(credit.Job) != "" {
			details.Crew = append(details.Crew, movieclub.Credit{PersonID: credit.ID, Name: strings.TrimSpace(credit.Name), Job: strings.TrimSpace(credit.Job)})
		}
	}
	return details, nil
}

func (c *Client) Recommendations(ctx context.Context, id int64) ([]movieclub.Movie, error) {
	return c.movieList(ctx, fmt.Sprintf("/movie/%d/recommendations?language=ru-RU&page=1", id))
}

func (c *Client) Similar(ctx context.Context, id int64) ([]movieclub.Movie, error) {
	return c.movieList(ctx, fmt.Sprintf("/movie/%d/similar?language=ru-RU&page=1", id))
}

func (c *Client) movieList(ctx context.Context, path string) ([]movieclub.Movie, error) {
	var payload struct {
		Results []movieResult `json:"results"`
	}
	if err := c.get(ctx, path, &payload); err != nil {
		return nil, err
	}
	return moviesFromResults(payload.Results), nil
}

func (c *Client) PersonMovies(ctx context.Context, id int64) ([]movieclub.PersonMovie, error) {
	if id <= 0 {
		return nil, errors.New("invalid person id")
	}
	var payload struct {
		Crew []struct {
			movieResult
			Job string `json:"job"`
		} `json:"crew"`
	}
	if err := c.get(ctx, fmt.Sprintf("/person/%d/movie_credits?language=ru-RU", id), &payload); err != nil {
		return nil, err
	}
	out := make([]movieclub.PersonMovie, 0, len(payload.Crew))
	for _, value := range payload.Crew {
		movies := moviesFromResults([]movieResult{value.movieResult})
		if len(movies) == 1 && strings.TrimSpace(value.Job) != "" {
			out = append(out, movieclub.PersonMovie{Movie: movies[0], Job: strings.TrimSpace(value.Job)})
		}
	}
	return out, nil
}

func moviesFromResults(values []movieResult) []movieclub.Movie {
	out := make([]movieclub.Movie, 0, len(values))
	for _, value := range values {
		title := strings.TrimSpace(value.Title)
		if value.Adult || value.ID <= 0 || title == "" || value.VoteCount < 0 || value.VoteAverage < 0 || value.VoteAverage > 10 || value.Popularity < 0 {
			continue
		}
		year := 0
		if len(value.ReleaseDate) >= 4 {
			year, _ = strconv.Atoi(value.ReleaseDate[:4])
		}
		overview := strings.TrimSpace(value.Overview)
		if utf8.RuneCountInString(overview) > 450 {
			runes := []rune(overview)
			overview = strings.TrimSpace(string(runes[:447])) + "…"
		}
		poster := ""
		if strings.HasPrefix(value.PosterPath, "/") && !strings.ContainsAny(value.PosterPath, "\r\n?#") {
			poster = value.PosterPath
		}
		out = append(out, movieclub.Movie{ID: value.ID, Title: title, Overview: overview, PosterPath: poster, Year: year, VoteCount: value.VoteCount, Rating: value.VoteAverage, Popularity: value.Popularity})
	}
	return out
}

func (c *Client) PosterURL(path string) string {
	if path == "" {
		return ""
	}
	c.mu.Lock()
	base := c.image
	c.mu.Unlock()
	if base == "" {
		base = "https://image.tmdb.org/t/p/w500"
	}
	return base + path
}

func (c *Client) LoadConfiguration(ctx context.Context) error {
	var payload struct {
		Images struct {
			SecureBaseURL string   `json:"secure_base_url"`
			PosterSizes   []string `json:"poster_sizes"`
		} `json:"images"`
	}
	if err := c.get(ctx, "/configuration", &payload); err != nil {
		return err
	}
	base, err := url.Parse(payload.Images.SecureBaseURL)
	if err != nil || base.Scheme != "https" || base.Host == "" {
		return errors.New("invalid tmdb image configuration")
	}
	found := false
	for _, size := range payload.Images.PosterSizes {
		found = found || size == "w500"
	}
	if !found {
		return errors.New("tmdb w500 poster size unavailable")
	}
	c.mu.Lock()
	c.image = strings.TrimRight(payload.Images.SecureBaseURL, "/") + "/w500"
	c.mu.Unlock()
	return nil
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	base := strings.TrimRight(c.BaseURL, "/")
	if base == "" || strings.TrimSpace(c.Token) == "" {
		return errors.New("tmdb is not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/json")
	started := time.Now()
	endpoint := endpointClass(path)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		c.log(endpoint, "request_failed", 0, time.Since(started))
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		c.log(endpoint, "http_error", resp.StatusCode, time.Since(started))
		return &HTTPError{Status: resp.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil || len(body) > maxResponseBytes {
		c.log(endpoint, "invalid_response", resp.StatusCode, time.Since(started))
		return errors.New("invalid tmdb response")
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	if err = dec.Decode(out); err != nil {
		c.log(endpoint, "invalid_response", resp.StatusCode, time.Since(started))
		return errors.New("invalid tmdb response")
	}
	var extra any
	if err = dec.Decode(&extra); !errors.Is(err, io.EOF) {
		c.log(endpoint, "invalid_response", resp.StatusCode, time.Since(started))
		return errors.New("invalid tmdb response")
	}
	c.log(endpoint, "ok", resp.StatusCode, time.Since(started))
	return nil
}

func endpointClass(path string) string {
	path = strings.SplitN(path, "?", 2)[0]
	switch {
	case path == "/configuration":
		return "configuration"
	case path == "/discover/movie":
		return "discover"
	case strings.HasSuffix(path, "/recommendations"):
		return "recommendations"
	case strings.HasSuffix(path, "/similar"):
		return "similar"
	case strings.HasSuffix(path, "/movie_credits"):
		return "person_movie_credits"
	case strings.HasPrefix(path, "/movie/"):
		return "movie_details"
	default:
		return "unknown"
	}
}

func (c *Client) log(endpoint, result string, status int, elapsed time.Duration) {
	if c.Log != nil {
		c.Log.Info("tmdb request completed", "endpoint", endpoint, "result", result, "status", status, "duration_ms", elapsed.Milliseconds())
	}
}

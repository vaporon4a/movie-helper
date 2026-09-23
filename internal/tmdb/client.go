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
}

func (c *Client) Discover(ctx context.Context, genreID int64, page, minVotes int) ([]movieclub.Movie, error) {
	if genreID <= 0 || page < 1 || page > 500 || minVotes < 0 {
		return nil, errors.New("invalid discover parameters")
	}
	q := url.Values{
		"language":         {"ru-RU"},
		"region":           {"RU"},
		"include_adult":    {"false"},
		"include_video":    {"false"},
		"sort_by":          {"popularity.desc"},
		"vote_average.gte": {"6"},
		"vote_count.gte":   {strconv.Itoa(minVotes)},
		"with_genres":      {strconv.FormatInt(genreID, 10)},
		"page":             {strconv.Itoa(page)},
	}
	var payload struct {
		Results []movieResult `json:"results"`
	}
	if err := c.get(ctx, "/discover/movie?"+q.Encode(), &payload); err != nil {
		return nil, err
	}
	out := make([]movieclub.Movie, 0, len(payload.Results))
	for _, value := range payload.Results {
		title := strings.TrimSpace(value.Title)
		if value.ID <= 0 || title == "" || value.VoteCount < 0 || value.VoteAverage < 0 || value.VoteAverage > 10 || value.Popularity < 0 {
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
	return out, nil
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
	resp, err := c.HTTP.Do(req)
	if err != nil {
		c.log("request_failed", 0, time.Since(started))
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		c.log("http_error", resp.StatusCode, time.Since(started))
		return &HTTPError{Status: resp.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil || len(body) > maxResponseBytes {
		c.log("invalid_response", resp.StatusCode, time.Since(started))
		return errors.New("invalid tmdb response")
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	if err = dec.Decode(out); err != nil {
		c.log("invalid_response", resp.StatusCode, time.Since(started))
		return errors.New("invalid tmdb response")
	}
	var extra any
	if err = dec.Decode(&extra); !errors.Is(err, io.EOF) {
		c.log("invalid_response", resp.StatusCode, time.Since(started))
		return errors.New("invalid tmdb response")
	}
	c.log("ok", resp.StatusCode, time.Since(started))
	return nil
}

func (c *Client) log(result string, status int, elapsed time.Duration) {
	if c.Log != nil {
		c.Log.Info("tmdb request completed", "result", result, "status", status, "duration_ms", elapsed.Milliseconds())
	}
}

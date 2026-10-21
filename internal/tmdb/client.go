// Package tmdb provides the small, validated subset of TMDB used by facts and watched-film profiles.
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
	"strings"
	"time"
)

const falseParam = "false"

const maxResponseBytes = 2 << 20

type HTTPError struct{ Status int }

func (e *HTTPError) Error() string { return fmt.Sprintf("tmdb http %d", e.Status) }

type Client struct {
	HTTP    *http.Client
	BaseURL string
	Token   string
	Log     *slog.Logger
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
	case path == "/discover/movie":
		return "discover"
	case path == "/search/movie":
		return "movie_search"
	case path == "/trending/movie/week":
		return "movie_trending"
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

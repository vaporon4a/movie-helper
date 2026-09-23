// Package meme adapts the public Meme API. It never fetches arbitrary user URLs.
package meme

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"unicode"

	"github.com/vaporon4a/movie-helper/internal/daily"
)

type Client struct {
	HTTP       *http.Client
	BaseURL    string
	Subreddits []string
}
type post struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Link    string `json:"postLink"`
	NSFW    bool   `json:"nsfw"`
	Spoiler bool   `json:"spoiler"`
	Ups     int    `json:"ups"`
}

func (c *Client) Candidates(ctx context.Context) ([]daily.Item, error) {
	var posts []post
	var lastErr error
	for _, sub := range c.Subreddits {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/gimme/"+url.PathEscape(sub)+"/10", nil)
		if err != nil {
			return nil, errors.New("invalid meme endpoint")
		}
		req.Header.Set("User-Agent", "movie-helper/0.1 (+https://github.com/vaporon4a/movie-helper)")
		r, err := c.HTTP.Do(req)
		if err != nil {
			lastErr = errors.New("meme provider unavailable")
			continue
		}
		if r.StatusCode != http.StatusOK {
			_ = r.Body.Close()
			lastErr = fmt.Errorf("meme provider status %d", r.StatusCode)
			continue
		}
		var body struct {
			Memes []post `json:"memes"`
		}
		err = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
		if closeErr := r.Body.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			lastErr = errors.New("invalid meme response")
			continue
		}
		posts = append(posts, body.Memes...)
	}
	sort.SliceStable(posts, func(i, j int) bool { return posts[i].Ups > posts[j].Ups })
	var out []daily.Item
	seen := make(map[string]bool)
	for _, p := range posts {
		if p.NSFW || p.Spoiler || !validImage(p.URL) || !validPost(p.Link) || !hasCyrillic(p.Title) || seen[p.Link] {
			continue
		}
		seen[p.Link] = true
		title := []rune(strings.TrimSpace(p.Title))
		if len(title) > 160 {
			title = title[:160]
		}
		out = append(out, daily.Item{Kind: daily.Meme, Text: string(title), Image: p.URL, Source: p.Link, Key: "reddit:" + p.Link})
	}
	if len(out) == 0 && lastErr != nil {
		return nil, lastErr
	}
	return out, nil
}
func validImage(raw string) bool {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.Host != "i.redd.it" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	ext := strings.ToLower(path.Ext(u.Path))
	return ext == ".jpg" || ext == ".jpeg" || ext == ".png"
}
func validPost(raw string) bool {
	u, e := url.Parse(raw)
	return e == nil && daily.ValidSource(raw) && (u.Host == "redd.it" || u.Host == "www.reddit.com" || u.Host == "reddit.com") && u.Path != ""
}
func hasCyrillic(s string) bool {
	for _, r := range s {
		if unicode.In(r, unicode.Cyrillic) {
			return true
		}
	}
	return false
}

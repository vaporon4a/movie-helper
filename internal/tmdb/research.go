package tmdb

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/vaporon4a/movie-helper/internal/clubprofile"
	"github.com/vaporon4a/movie-helper/internal/filmresearch"
)

// ResearchSources fixes identity before collecting opinions. A review is never
// treated as a plot synopsis, and user-supplied review links are not fetched.
func (c *Client) ResearchSources(ctx context.Context, expected clubprofile.Film, now time.Time) ([]filmresearch.SourceSnapshot, error) {
	f, err := c.FilmDetails(ctx, expected.TMDBID)
	if err != nil {
		return nil, err
	}
	if f.Year != expected.Year || f.Year == 0 {
		return nil, filmresearch.ErrInvalid
	}
	var out []filmresearch.SourceSnapshot
	if text := strings.TrimSpace(f.Metadata.Overview); text != "" {
		out = append(out, researchSource(f.TMDBID, "plot", "ru", fmt.Sprintf("https://www.themoviedb.org/movie/%d", f.TMDBID), text, now))
	}
	out = append(out, c.researchOpinions(ctx, f.TMDBID, now)...)
	if len(out) == 0 {
		return nil, filmresearch.ErrNotFound
	}
	return out, nil
}
func researchSource(id int64, kind, language, link, text string, now time.Time) filmresearch.SourceSnapshot {
	if len(text) > 4000 {
		text = text[:4000]
		for !utf8.ValidString(text) {
			text = text[:len(text)-1]
		}
	}
	return filmresearch.SourceSnapshot{Version: filmresearch.SourceV1, TMDBID: id, Kind: kind, URL: link, Language: language, Attribution: "TMDB", Text: text, Spans: []filmresearch.EvidenceSpan{{ID: "text", Start: 0, End: len(text)}}, FetchedAt: now}
}

func (c *Client) researchOpinions(ctx context.Context, id int64, now time.Time) []filmresearch.SourceSnapshot {
	var out []filmresearch.SourceSnapshot
	var reviews struct {
		ID      int64 `json:"id"`
		Results []struct {
			ID      string `json:"id"`
			Content string `json:"content"`
		} `json:"results"`
	}
	// Opinions are optional. Failure does not discard an identified synopsis.
	if err := c.get(ctx, fmt.Sprintf("/movie/%d/reviews?language=en-US&page=1", id), &reviews); err == nil && reviews.ID == id {
		for _, review := range reviews.Results {
			if strings.TrimSpace(review.Content) == "" || review.ID == "" {
				continue
			}
			out = append(out, researchSource(id, "opinion", "en", "https://www.themoviedb.org/review/"+url.PathEscape(review.ID), review.Content, now))
			if len(out) >= 2 {
				break
			}
		}
	}

	return out
}

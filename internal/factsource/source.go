// Package factsource resolves TMDB identities to revision-bound Russian Wikipedia stories.
package factsource

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/vaporon4a/movie-helper/internal/facts"
)

type ExternalIDs interface {
	FactExternalIDs(context.Context, int64) (facts.ExternalIDs, error)
}
type Source struct {
	Log                       *slog.Logger
	HTTP                      *http.Client
	IDs                       ExternalIDs
	WikidataURL, WikipediaURL string
}

var qidPattern = regexp.MustCompile(`^Q[1-9][0-9]*$`)
var revisionPattern = regexp.MustCompile(`"wgRevisionId"\s*:\s*([0-9]+)`)
var wikiIDPattern = regexp.MustCompile(`"wgWikibaseItemId"\s*:\s*"(Q[0-9]+)"`)

func (s *Source) Episodes(ctx context.Context, movie facts.Movie) ([]facts.Episode, error) {
	episodes, err := s.episodes(ctx, movie)
	result := "resolved"
	if err != nil {
		result = "source_failed"
	} else if len(episodes) == 0 {
		result = "identity_or_no_episode"
	}
	if s.Log != nil {
		s.Log.Info("fact source resolved", "movie_id", movie.ID, "result", result, "episodes", len(episodes))
	}
	return episodes, err
}
func (s *Source) episodes(ctx context.Context, movie facts.Movie) ([]facts.Episode, error) {
	ids, err := s.IDs.FactExternalIDs(ctx, movie.ID)
	if err != nil {
		return nil, err
	}
	if !qidPattern.MatchString(ids.Wikidata) {
		return nil, nil
	}
	raw, err := s.get(ctx, s.WikidataURL+"/wiki/Special:EntityData/"+ids.Wikidata+".json")
	if err != nil {
		return nil, err
	}
	var data struct {
		Entities map[string]entity `json:"entities"`
	}
	if err = json.Unmarshal(raw, &data); err != nil {
		return nil, errors.New("invalid fact identity response")
	}
	title, ok := resolve(movie, ids, data.Entities[ids.Wikidata])
	if !ok {
		return nil, nil
	}
	raw, err = s.get(ctx, s.WikipediaURL+"/wiki/"+url.PathEscape(strings.ReplaceAll(title, " ", "_")))
	if err != nil {
		return nil, err
	}
	return extract(raw, movie, ids.Wikidata)
}
func (s *Source) get(ctx context.Context, address string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, errors.New("invalid fact source request")
	}
	req.Header.Set("User-Agent", "MovieHelper/1.0 (https://github.com/vaporon4a/movie-helper)")
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return nil, errors.New("fact source connection failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fact source http %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
	if err != nil || len(raw) > 4<<20 {
		return nil, errors.New("fact source response too large")
	}
	return raw, nil
}

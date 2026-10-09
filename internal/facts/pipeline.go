// Package facts selects sourced Russian movie stories; adapters own HTTP and SQL.
package facts

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/vaporon4a/movie-helper/internal/daily"
)

type ExternalIDs struct{ Wikidata, IMDb string }

type Movie struct {
	ID                   int64
	Title, OriginalTitle string
	Year                 int
}

type Episode struct {
	Key                            string
	Movie                          Movie
	Text, Section, URL, WikidataID string
	RevisionID                     int64
}

type Cache interface {
	LoadFactCache(context.Context, string, time.Time) ([]byte, bool, error)
	SaveFactCache(context.Context, string, []byte, time.Time) error
}

type Rotation interface {
	PickFactMovies(context.Context, int64, []Movie, time.Time, int) ([]Movie, error)
	Seen(context.Context, int64, string, string) (bool, error)
}

type Catalog interface {
	FactMovies(context.Context, time.Time) ([]Movie, error)
}
type Source interface {
	Episodes(context.Context, Movie) ([]Episode, error)
}
type Selector interface {
	SelectEpisodes(context.Context, []Episode) ([]daily.Item, error)
}

type Pipeline struct {
	Catalog  Catalog
	Source   Source
	Cache    Cache
	History  Rotation
	Selector Selector
	Now      func() time.Time
	Log      *slog.Logger
}

func (p *Pipeline) Candidates(ctx context.Context, chat int64) ([]daily.Item, error) {
	if p.Catalog == nil || p.Selector == nil {
		return nil, nil
	}
	now := p.Now()
	key := fmt.Sprintf("fact-pending-v1:%d", chat)
	var episodes []Episode
	cached, err := p.load(ctx, key, now, &episodes)
	if err != nil {
		return nil, err
	}
	if !cached {
		episodes, err = p.nextEpisodes(ctx, chat, now)
		if err != nil {
			return nil, err
		}
		if len(episodes) == 0 {
			return nil, nil
		}
		if err = p.save(ctx, key, episodes, now.Add(24*time.Hour)); err != nil {
			return nil, err
		}
	}
	if p.Log != nil {
		p.Log.Info("fact source batch", "chat_id", chat, "episodes", len(episodes), "cached", cached, "policy", daily.FactPolicy)
	}
	selected, err := p.Selector.SelectEpisodes(ctx, episodes)
	if err != nil {
		return nil, err
	}
	// A valid rejection advances rotation; transient provider failures keep the batch.
	if err = p.save(ctx, key, []Episode{}, now); err != nil {
		return nil, err
	}
	return selected, nil
}

func (p *Pipeline) catalog(ctx context.Context, now time.Time) ([]Movie, error) {
	const key = "fact-catalog-ru-v1"
	var movies []Movie
	ok, err := p.load(ctx, key, now, &movies)
	if err != nil || ok {
		return movies, err
	}
	movies, err = p.Catalog.FactMovies(ctx, now)
	if err != nil {
		// A bounded stale catalog is safe: film identity is validated separately.
		var stale []Movie
		ok, cacheErr := p.load(ctx, key, now.Add(-6*24*time.Hour), &stale)
		if cacheErr != nil {
			return nil, cacheErr
		}
		if ok {
			return stale, nil
		}
		return nil, err
	}
	if len(movies) == 0 {
		return nil, fmt.Errorf("fact catalog empty")
	}
	return movies, p.save(ctx, key, movies, now.Add(24*time.Hour))
}

func (p *Pipeline) unseenEpisodes(ctx context.Context, chat int64, movies []Movie, now time.Time) ([]Episode, error) {
	var all []Episode
	var lastErr error
	for _, movie := range movies {
		episodes, err := p.episodes(ctx, movie, now)
		if err != nil {
			lastErr = err
			continue
		}
		added := 0
		for _, episode := range spread(episodes, 3) {
			seen, err := p.History.Seen(ctx, chat, daily.Fact, episode.Key)
			if err != nil {
				return nil, err
			}
			if !seen {
				all = append(all, episode)
				added++
			}
			if added == 3 {
				break
			}
		}
	}
	if len(all) == 0 {
		return nil, lastErr
	}
	return all, nil
}

func (p *Pipeline) episodes(ctx context.Context, movie Movie, now time.Time) ([]Episode, error) {
	key := fmt.Sprintf("fact-source-ru-v1:%d", movie.ID)
	var episodes []Episode
	ok, err := p.load(ctx, key, now, &episodes)
	if err != nil || ok {
		return episodes, err
	}
	episodes, err = p.Source.Episodes(ctx, movie)
	if err != nil {
		return nil, err
	}
	ttl := 14 * 24 * time.Hour
	if len(episodes) == 0 {
		ttl = 24 * time.Hour
	}
	return episodes, p.save(ctx, key, episodes, now.Add(ttl))
}
func (p *Pipeline) load(ctx context.Context, key string, now time.Time, out any) (bool, error) {
	raw, ok, err := p.Cache.LoadFactCache(ctx, key, now)
	if err != nil || !ok {
		return false, err
	}
	if err = json.Unmarshal(raw, out); err != nil {
		return false, fmt.Errorf("invalid fact cache")
	}
	return true, nil
}
func (p *Pipeline) save(ctx context.Context, key string, value any, expires time.Time) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return p.Cache.SaveFactCache(ctx, key, raw, expires)
}
func EpisodeKey(id int64, text string) string {
	return fmt.Sprintf("factru:%d:%x", id, sha256.Sum256([]byte(text)))
}
func spread(values []Episode, limit int) []Episode {
	if len(values) <= limit {
		return values
	}
	out := make([]Episode, 0, limit)
	for n := 0; n < limit; n++ {
		out = append(out, values[n*(len(values)-1)/(limit-1)])
	}
	return out
}

func (p *Pipeline) nextEpisodes(ctx context.Context, chat int64, now time.Time) ([]Episode, error) {
	movies, err := p.catalog(ctx, now)
	if err != nil {
		return nil, err
	}
	movies, err = p.History.PickFactMovies(ctx, chat, movies, now, 4)
	if err != nil {
		return nil, err
	}
	return p.unseenEpisodes(ctx, chat, movies, now)
}

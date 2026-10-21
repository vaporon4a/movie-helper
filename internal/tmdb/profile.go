package tmdb

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/vaporon4a/movie-helper/internal/clubprofile"
)

type profileMovie struct {
	ID               int64                 `json:"id"`
	Title            string                `json:"title"`
	OriginalTitle    string                `json:"original_title"`
	ReleaseDate      string                `json:"release_date"`
	Adult            bool                  `json:"adult"`
	Overview         string                `json:"overview"`
	Genres           []clubprofile.FilmTag `json:"genres"`
	Runtime          *int                  `json:"runtime"`
	OriginalLanguage string                `json:"original_language"`
	Countries        []struct {
		Code string `json:"iso_3166_1"`
	} `json:"production_countries"`
	Spoken []struct {
		Code string `json:"iso_639_1"`
	} `json:"spoken_languages"`
	Credits struct {
		Crew []clubprofile.FilmCredit `json:"crew"`
	} `json:"credits"`
	Keywords struct {
		Keywords []clubprofile.FilmTag `json:"keywords"`
	} `json:"keywords"`
}

func (p profileMovie) film(full bool) clubprofile.Film {
	f := clubprofile.Film{TMDBID: p.ID, Title: strings.TrimSpace(p.Title), OriginalTitle: p.OriginalTitle, Source: "tmdb", FetchedAt: time.Now()}
	if d, err := time.Parse(time.DateOnly, p.ReleaseDate); err == nil {
		f.Year = d.Year()
	}
	if full {
		f.Metadata = clubprofile.FilmMetadata{Overview: p.Overview, Genres: p.Genres, Runtime: p.Runtime, OriginalLanguage: p.OriginalLanguage, Credits: p.Credits.Crew, Keywords: p.Keywords.Keywords, Complete: true}
		for _, country := range p.Countries {
			f.Metadata.Countries = append(f.Metadata.Countries, country.Code)
		}
		for _, language := range p.Spoken {
			f.Metadata.SpokenLanguages = append(f.Metadata.SpokenLanguages, language.Code)
		}
	}
	return f
}

func (c *Client) SearchFilms(ctx context.Context, query string) ([]clubprofile.Film, error) {
	if strings.TrimSpace(query) == "" {
		return nil, clubprofile.ErrInvalid
	}
	var payload struct {
		Results []profileMovie `json:"results"`
	}
	q := url.Values{"query": {query}, "language": {"ru-RU"}, "include_adult": {falseParam}, "page": {"1"}}
	if err := c.get(ctx, "/search/movie?"+q.Encode(), &payload); err != nil {
		return nil, err
	}
	out := make([]clubprofile.Film, 0, 5)
	seen := map[int64]bool{}
	for _, p := range payload.Results {
		if p.ID <= 0 || p.Adult || strings.TrimSpace(p.Title) == "" || seen[p.ID] {
			continue
		}
		out = append(out, p.film(false))
		seen[p.ID] = true
		if len(out) == 5 {
			break
		}
	}
	return out, nil
}

func (c *Client) FilmDetails(ctx context.Context, id int64) (clubprofile.Film, error) {
	if id <= 0 {
		return clubprofile.Film{}, clubprofile.ErrInvalid
	}
	var p profileMovie
	if err := c.get(ctx, fmt.Sprintf("/movie/%d?language=ru-RU&append_to_response=credits,keywords", id), &p); err != nil {
		return clubprofile.Film{}, err
	}
	if p.ID != id || p.Adult || strings.TrimSpace(p.Title) == "" {
		return clubprofile.Film{}, clubprofile.ErrInvalid
	}
	return p.film(true), nil
}

func (c *Client) FilmIdentity(ctx context.Context, id int64) (clubprofile.Film, error) {
	if id <= 0 {
		return clubprofile.Film{}, clubprofile.ErrInvalid
	}
	var p profileMovie
	if err := c.get(ctx, fmt.Sprintf("/movie/%d?language=ru-RU", id), &p); err != nil {
		return clubprofile.Film{}, err
	}
	if p.ID != id || p.Adult || strings.TrimSpace(p.Title) == "" {
		return clubprofile.Film{}, clubprofile.ErrInvalid
	}
	f := p.film(true)
	f.Metadata.Complete = false
	return f, nil
}

package clubprofile

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type FilmCredit struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Job  string `json:"job"`
}

type FilmTag struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// Nil fields mean unavailable; original language is not the language of a screening.
type FilmMetadata struct {
	Overview         string       `json:"overview,omitempty"`
	Genres           []FilmTag    `json:"genres"`
	Countries        []string     `json:"countries"`
	OriginalLanguage string       `json:"original_language,omitempty"`
	SpokenLanguages  []string     `json:"spoken_languages"`
	Runtime          *int         `json:"runtime_minutes"`
	Credits          []FilmCredit `json:"credits"`
	Keywords         []FilmTag    `json:"keywords"`
	Complete         bool         `json:"complete"`
}

type WatchInput struct {
	Provisional           bool `json:"provisional,omitempty"`
	Query                 string
	TMDBID                int64
	Date, Precision, Zone string
}

type WatchDraft struct {
	ID, ChatID, WatchID  int64
	Input                WatchInput
	Candidates           []Film
	State, Reason        string
	Expires, NextAttempt time.Time
	MessageID            int
	Closed               bool
	Version, Rendered    int
}

func ParseWatchInput(text, zone string, now time.Time) (WatchInput, error) {
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return WatchInput{}, ErrInvalid
	}
	parts := strings.Split(text, "|")
	if len(parts) > 2 || strings.TrimSpace(parts[0]) == "" {
		return WatchInput{}, ErrInvalid
	}
	in := WatchInput{Query: strings.TrimSpace(parts[0]), Zone: zone, Date: now.In(loc).Format(time.DateOnly), Precision: "day"}
	if len([]rune(in.Query)) > 250 {
		return WatchInput{}, ErrInvalid
	}
	if len(parts) == 2 {
		in.Date, in.Precision, err = parseWatchDate(strings.TrimSpace(parts[1]), now.In(loc))
		if err != nil {
			return WatchInput{}, err
		}
	}
	if strings.Contains(in.Query, "://") {
		in.TMDBID, err = ParseFilmURL(in.Query)
		if err != nil {
			return WatchInput{}, err
		}
		in.Query = ""
	}
	return in, nil
}

func parseWatchDate(value string, now time.Time) (string, string, error) {
	if value == "unknown" {
		return "", "unknown", nil
	}
	for _, f := range []struct{ layout, precision string }{{time.DateOnly, "day"}, {"2006-01", "month"}, {"2006", "year"}} {
		d, err := time.ParseInLocation(f.layout, value, now.Location())
		if err == nil && !d.After(now) && d.Year() >= 1870 {
			return value, f.precision, nil
		}
	}
	return "", "", ErrInvalid
}

func ParseFilmURL(raw string) (int64, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || (u.Host != "www.themoviedb.org" && u.Host != "themoviedb.org") {
		return 0, ErrInvalid
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 2 || parts[0] != "movie" {
		return 0, ErrInvalid
	}
	id, err := strconv.ParseInt(strings.SplitN(parts[1], "-", 2)[0], 10, 64)
	if err != nil || id <= 0 {
		return 0, ErrInvalid
	}
	return id, nil
}

func (f Film) Label() string {
	if f.Year > 0 {
		return fmt.Sprintf("%s (%d)", f.Title, f.Year)
	}
	return f.Title
}

// FreshForReview controls enrollment only; time spent in the queue is unlimited.
func FreshForReview(in WatchInput, now time.Time) bool {
	if in.Precision != "day" {
		return false
	}
	loc, err := time.LoadLocation(in.Zone)
	if err != nil {
		return false
	}
	d, err := time.ParseInLocation(time.DateOnly, in.Date, loc)
	if err != nil || d.After(now) {
		return false
	}
	return now.Before(d.AddDate(0, 0, 14))
}

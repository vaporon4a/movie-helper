package factsource

import (
	"encoding/json"
	"strconv"
	"strings"
	"unicode"

	"github.com/vaporon4a/movie-helper/internal/facts"
)

type entity struct {
	ID     string `json:"id"`
	Labels map[string]struct {
		Value string `json:"value"`
	} `json:"labels"`
	Aliases map[string][]struct {
		Value string `json:"value"`
	} `json:"aliases"`
	Sitelinks map[string]struct {
		Title string `json:"title"`
	} `json:"sitelinks"`
	Claims map[string][]struct {
		Rank string `json:"rank"`
		Main struct {
			Data struct {
				Value json.RawMessage `json:"value"`
			} `json:"datavalue"`
		} `json:"mainsnak"`
	} `json:"claims"`
}

func resolve(movie facts.Movie, ids facts.ExternalIDs, e entity) (string, bool) {
	if e.ID != ids.Wikidata || !exactID(e, "P4947", strconv.FormatInt(movie.ID, 10), true) || !exactID(e, "P345", ids.IMDb, false) {
		return "", false
	}
	if !titleMatches(movie, e) || !yearMatches(movie.Year, e) {
		return "", false
	}
	title := e.Sitelinks["ruwiki"].Title
	return title, title != ""
}
func exactID(e entity, property, wanted string, required bool) bool {
	if wanted == "" {
		return !required
	}
	found := false
	for _, claim := range e.Claims[property] {
		if claim.Rank == "deprecated" {
			continue
		}
		var value string
		if json.Unmarshal(claim.Main.Data.Value, &value) != nil {
			return false
		}
		if value != wanted {
			return false
		}
		found = true
	}
	return found || !required
}
func titleMatches(m facts.Movie, e entity) bool {
	for _, lang := range []string{"ru", "en"} {
		if sameTitle(m, e.Labels[lang].Value) {
			return true
		}
		for _, alias := range e.Aliases[lang] {
			if sameTitle(m, alias.Value) {
				return true
			}
		}
	}
	return false
}
func sameTitle(m facts.Movie, title string) bool {
	n := normalizedTitle(title)
	return n != "" && (n == normalizedTitle(m.Title) || n == normalizedTitle(m.OriginalTitle))
}
func normalizedTitle(s string) string {
	if at := strings.Index(s, " ("); at >= 0 {
		s = s[:at]
	}
	var out strings.Builder
	for _, r := range strings.ToLower(s) {
		if r == 'ё' {
			r = 'е'
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			out.WriteRune(r)
		}
	}
	return out.String()
}
func yearMatches(year int, e entity) bool {
	for _, claim := range e.Claims["P577"] {
		if claim.Rank == "deprecated" {
			continue
		}
		var value struct {
			Time string `json:"time"`
		}
		if json.Unmarshal(claim.Main.Data.Value, &value) != nil || len(value.Time) < 5 {
			continue
		}
		candidate, err := strconv.Atoi(value.Time[1:5])
		if err == nil && candidate >= year-1 && candidate <= year+1 {
			return true
		}
	}
	return false
}

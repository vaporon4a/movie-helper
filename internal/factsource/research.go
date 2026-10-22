package factsource

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/vaporon4a/movie-helper/internal/clubprofile"
	"github.com/vaporon4a/movie-helper/internal/facts"
	"github.com/vaporon4a/movie-helper/internal/filmresearch"
	"golang.org/x/net/html"
)

var researchSections = regexp.MustCompile(`(?i)сюжет|критик|отзыв|тематика`)

func (s *Source) ResearchSources(ctx context.Context, f clubprofile.Film, now time.Time) ([]filmresearch.SourceSnapshot, error) {
	raw, qid, err := s.article(ctx, facts.Movie{ID: f.TMDBID, Title: f.Title, OriginalTitle: f.OriginalTitle, Year: f.Year})
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, filmresearch.ErrNotFound
	}
	return researchExtract(raw, qid, f.TMDBID, now)
}
func researchExtract(raw []byte, qid string, id int64, now time.Time) ([]filmresearch.SourceSnapshot, error) {
	identity := wikiIDPattern.FindSubmatch(raw)
	revision := revisionPattern.FindSubmatch(raw)
	if len(identity) != 2 || string(identity[1]) != qid || len(revision) != 2 {
		return nil, filmresearch.ErrInvalid
	}
	rev, err := strconv.ParseInt(string(revision[1]), 10, 64)
	if err != nil || rev <= 0 {
		return nil, filmresearch.ErrInvalid
	}
	tree, err := html.Parse(bytes.NewReader(raw))
	if err != nil {
		return nil, filmresearch.ErrInvalid
	}
	p := paragraphs{sections: researchSections}
	p.walk(tree)
	return researchParagraphs(p.values, id, rev, now)
}
func researchParagraphs(values []passage, id, rev int64, now time.Time) ([]filmresearch.SourceSnapshot, error) {
	var out []filmresearch.SourceSnapshot
	// Only the opening two plot paragraphs; later events are not needed to ask
	// about a premise. The semantic audit still checks each question for spoilers.
	plots, opinions := 0, 0
	for _, v := range values {
		if len(v.text) < 80 || len(v.text) > 6000 {
			continue
		}
		kind := "opinion"
		if strings.Contains(strings.ToLower(v.section), "сюжет") {
			kind = "plot"
			plots++
			if plots > 2 {
				continue
			}
		} else {
			opinions++
			if opinions > 2 {
				continue
			}
		}
		out = append(out, filmresearch.SourceSnapshot{Version: filmresearch.SourceV1, TMDBID: id, Kind: kind, URL: fmt.Sprintf("https://ru.wikipedia.org/w/index.php?oldid=%d", rev), Revision: strconv.FormatInt(rev, 10), Language: "ru", Attribution: "Wikipedia contributors", License: "CC BY-SA 4.0", Text: v.text, Spans: []filmresearch.EvidenceSpan{{ID: "paragraph", End: len(v.text)}}, FetchedAt: now})
	}
	if len(out) == 0 {
		return nil, filmresearch.ErrNotFound
	}
	return out, nil
}

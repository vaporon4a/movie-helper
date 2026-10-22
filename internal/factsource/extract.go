package factsource

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/vaporon4a/movie-helper/internal/facts"
	"golang.org/x/net/html"
)

var productionSection = regexp.MustCompile(`(?i)создан|съ[её]м|производ|разработ|сценари|спецэфф|кастинг|работа над фильмом|интересные факты|звук|музык`)

type paragraphs struct {
	sections            *regexp.Regexp
	section, subsection string
	values              []passage
}
type passage struct{ section, text string }

func extract(raw []byte, movie facts.Movie, qid string) ([]facts.Episode, error) {
	match := wikiIDPattern.FindSubmatch(raw)
	revision := revisionPattern.FindSubmatch(raw)
	if len(match) != 2 || string(match[1]) != qid || len(revision) != 2 {
		return nil, errors.New("fact article identity mismatch")
	}
	revisionID, err := strconv.ParseInt(string(revision[1]), 10, 64)
	if err != nil || revisionID <= 0 {
		return nil, errors.New("missing fact article revision")
	}
	root, err := html.Parse(bytes.NewReader(raw))
	if err != nil {
		return nil, errors.New("invalid fact article html")
	}
	p := &paragraphs{}
	p.walk(root)
	var out []facts.Episode
	unique := map[string]bool{}
	for _, value := range p.values {
		for _, text := range episodeWindows(value.text) {
			if !eligible(text) {
				continue
			}
			key := facts.EpisodeKey(movie.ID, text)
			if unique[key] {
				continue
			}
			unique[key] = true
			out = append(out, facts.Episode{Key: key, Movie: movie, Text: text, Section: value.section,
				URL: fmt.Sprintf("https://ru.wikipedia.org/w/index.php?oldid=%d", revisionID), WikidataID: qid, RevisionID: revisionID})
		}
	}
	return out, nil
}
func (p *paragraphs) walk(n *html.Node) {
	if hidden(n) {
		return
	}
	if n.Type == html.ElementNode {
		switch n.Data {
		case "h2":
			p.section = nodeText(n)
			p.subsection = ""
		case "h3", "h4":
			p.subsection = nodeText(n)
		case "p":
			sections := p.sections
			if sections == nil {
				sections = productionSection
			}
			if sections.MatchString(p.section) {
				p.values = append(p.values, passage{section: p.section + " · " + p.subsection, text: nodeText(n)})
			}
			return
		}
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		p.walk(child)
	}
}
func hidden(n *html.Node) bool {
	if n.Type != html.ElementNode {
		return false
	}
	switch n.Data {
	case "script", "style", "sup", "table", "figure", "nav":
		return true
	}
	for _, a := range n.Attr {
		if a.Key == "class" && (strings.Contains(a.Val, "mw-editsection") || strings.Contains(a.Val, "reference")) {
			return true
		}
	}
	return false
}
func nodeText(n *html.Node) string {
	var out strings.Builder
	appendText(&out, n)
	return strings.Join(strings.Fields(out.String()), " ")
}
func appendText(out *strings.Builder, n *html.Node) {
	if hidden(n) {
		return
	}
	if n.Type == html.TextNode {
		out.WriteString(n.Data)
	}
	if n.Type == html.ElementNode && n.Data == "br" {
		out.WriteByte(' ')
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		appendText(out, child)
	}
}
func eligible(text string) bool {
	words := len(strings.Fields(text))
	if words < 8 || words > 140 || utf8.RuneCountInString(text) > 1050 || !strings.ContainsAny(text, ".!?") {
		return false
	}
	last := []rune(text)[utf8.RuneCountInString(text)-1]
	if !strings.ContainsRune(".!?»”", last) {
		return false
	}
	cyrillic := 0
	for _, r := range text {
		if unicode.In(r, unicode.Cyrillic) {
			cyrillic++
		}
	}
	return cyrillic > 20 && !strings.ContainsAny(text, "{}<>")
}
func episodeWindows(text string) []string {
	if eligible(text) {
		return []string{text}
	}
	sentences := splitSentences(text)
	var out []string
	for start := range sentences {
		for length := 1; length <= 4 && start+length <= len(sentences); length++ {
			window := strings.Join(sentences[start:start+length], " ")
			if eligible(window) {
				out = append(out, window)
			}
		}
	}
	return out
}

// Windows retain exact contiguous source text; initial/abbreviation boundaries are skipped.
func splitSentences(text string) []string {
	runes := []rune(text)
	var out []string
	start := 0
	for n, r := range runes {
		if !strings.ContainsRune(".!?", r) || n+2 >= len(runes) || runes[n+1] != ' ' || !unicode.IsUpper(runes[n+2]) {
			continue
		}
		before := strings.Fields(string(runes[start:n]))
		if len(before) == 0 || utf8.RuneCountInString(before[len(before)-1]) <= 2 {
			continue
		}
		out = append(out, strings.TrimSpace(string(runes[start:n+1])))
		start = n + 2
	}
	if start < len(runes) {
		out = append(out, strings.TrimSpace(string(runes[start:])))
	}
	return out
}

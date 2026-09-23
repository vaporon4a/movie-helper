package content

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/vaporon4a/movie-helper/internal/daily"
	"github.com/vaporon4a/movie-helper/internal/gemini"
)

// Wikipedia provides production sections, with attribution to a specific revision.
// Titles are configuration, never user-supplied URLs. One fact per title per chat.
type Wikipedia struct {
	HTTP      *http.Client
	Endpoint  string
	Titles    []string
	mu        sync.Mutex
	remaining map[int64][]string
}

func (w *Wikipedia) Articles(ctx context.Context, chat int64, history History) ([]gemini.Article, error) {
	var articles []gemini.Article
	if len(w.Titles) == 0 {
		return articles, nil
	}
	var eligible []string
	for _, title := range w.Titles {
		key := "wikipedia:" + title
		seen, err := history.Seen(ctx, chat, daily.Fact, key)
		if err != nil {
			return nil, err
		}
		if seen {
			continue
		}
		eligible = append(eligible, title)
	}
	var lastErr error
	for _, title := range w.takeTitles(chat, eligible) {
		a, err := w.article(ctx, title)
		if err != nil {
			lastErr = err
			continue
		}
		if a != nil {
			a.Key = "wikipedia:" + title
			articles = append(articles, *a)
		}
	}
	if len(articles) == 0 {
		return nil, lastErr
	}
	return articles, nil
}

// Reserve distinct titles before fetching so concurrent previews and retries
// advance the same chat's shuffled cycle. Publication history remains in SQLite;
// this temporary rotation restarts when the process restarts.
func (w *Wikipedia) takeTitles(chat int64, eligible []string) []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.remaining == nil {
		w.remaining = make(map[int64][]string)
	}
	valid := make(map[string]bool, len(eligible))
	var unique []string
	for _, title := range eligible {
		if !valid[title] {
			valid[title] = true
			unique = append(unique, title)
		}
	}
	var remaining []string
	for _, title := range w.remaining[chat] {
		if valid[title] {
			remaining = append(remaining, title)
		}
	}
	if len(remaining) == 0 {
		remaining = unique
		rand.Shuffle(len(remaining), func(i, j int) {
			remaining[i], remaining[j] = remaining[j], remaining[i]
		})
	}
	n := min(3, len(remaining))
	w.remaining[chat] = remaining[n:]
	return remaining[:n]
}

func (w *Wikipedia) article(ctx context.Context, title string) (*gemini.Article, error) {
	u, err := url.Parse(w.Endpoint)
	if err != nil {
		return nil, errors.New("invalid Wikipedia endpoint")
	}
	q := u.Query()
	for k, v := range map[string]string{"action": "query", "prop": "extracts|revisions", "explaintext": "1", "exsectionformat": "wiki", "redirects": "1", "titles": title, "format": "json", "formatversion": "2", "rvprop": "ids"} {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, errors.New("invalid Wikipedia request")
	}
	req.Header.Set("User-Agent", "MovieHelper/0.1 (https://github.com/vaporon4a/movie-helper)")
	r, err := w.HTTP.Do(req)
	if err != nil {
		return nil, errors.New("Wikipedia unavailable")
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusOK {
		return nil, errors.New("Wikipedia status")
	}
	var response struct {
		Query struct {
			Pages []struct {
				Title     string `json:"title"`
				Extract   string `json:"extract"`
				Revisions []struct {
					ID int64 `json:"revid"`
				} `json:"revisions"`
			} `json:"pages"`
		} `json:"query"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&response); err != nil {
		return nil, errors.New("invalid Wikipedia response")
	}
	if len(response.Query.Pages) != 1 {
		return nil, nil
	}
	p := response.Query.Pages[0]
	if len(p.Revisions) != 1 || p.Revisions[0].ID <= 0 {
		return nil, nil
	}
	text := production(p.Extract)
	if len([]rune(text)) < 100 {
		return nil, nil
	}
	return &gemini.Article{Title: p.Title, Text: text,
		URL:         fmt.Sprintf("https://en.wikipedia.org/w/index.php?oldid=%d", p.Revisions[0].ID),
		Attribution: "По материалам Wikipedia, переработано AI. CC BY-SA 4.0: https://creativecommons.org/licenses/by-sa/4.0/",
	}, nil
}

func production(extract string) string {
	var lines []string
	inside := false
	for line := range strings.SplitSeq(extract, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "== ") && strings.HasSuffix(line, " ==") {
			if inside {
				break
			}
			heading := strings.ToLower(strings.Trim(line, "= "))
			inside = heading == "production" || heading == "development" || heading == "filming"
			continue
		}
		if inside && !strings.HasPrefix(line, "===") {
			lines = append(lines, line)
		}
	}
	text := []rune(strings.Join(strings.Fields(strings.Join(lines, " ")), " "))
	if len(text) > 5000 {
		text = text[:5000]
	}
	return string(text)
}

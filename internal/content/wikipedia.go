package content

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/vaporon4a/movie-helper/internal/daily"
	"github.com/vaporon4a/movie-helper/internal/gemini"
)

// Wikipedia provides production sections, with attribution to a specific revision.
// Titles are configuration, never user-supplied URLs. One fact per title per chat.
type Wikipedia struct {
	HTTP     *http.Client
	Endpoint string
	Titles   []string
	Now      func() time.Time
}

func (w *Wikipedia) Articles(ctx context.Context, chat int64, history History) ([]gemini.Article, error) {
	var articles []gemini.Article
	if len(w.Titles) == 0 {
		return articles, nil
	}
	start := int(w.Now().UTC().Unix()/86400) % len(w.Titles)
	var lastErr error
	attempts := 0
	for n := 0; n < len(w.Titles) && attempts < 3; n++ {
		title := w.Titles[(start+n)%len(w.Titles)]
		key := "wikipedia:" + title
		seen, err := history.Seen(ctx, chat, daily.Fact, key)
		if err != nil {
			return nil, err
		}
		if seen {
			continue
		}
		attempts++
		a, err := w.article(ctx, title)
		if err != nil {
			lastErr = err
			continue
		}
		if a != nil {
			a.Key = key
			articles = append(articles, *a)
		}
	}
	if len(articles) == 0 {
		return nil, lastErr
	}
	return articles, nil
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
	for _, line := range strings.Split(extract, "\n") {
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

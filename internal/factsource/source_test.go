package factsource

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/vaporon4a/movie-helper/internal/facts"
	"github.com/vaporon4a/movie-helper/internal/tmdb"
)

func TestStrictIdentity(t *testing.T) {
	raw := `{"id":"Q123","labels":{"ru":{"value":"Матрица"}},"claims":{"P4947":[{"mainsnak":{"datavalue":{"value":"603"}}}],"P345":[{"mainsnak":{"datavalue":{"value":"tt0133093"}}}],"P577":[{"mainsnak":{"datavalue":{"value":{"time":"+1999-03-31T00:00:00Z"}}}}]},"sitelinks":{"ruwiki":{"title":"Матрица (фильм)"}}}`
	for _, replace := range [][2]string{{"", ""}, {"Q123", "Q124"}, {"603", "999"}, {"tt0133093", "tt0000000"}, {"1999", "1980"}, {"Матрица\"", "Другой фильм\""}} {
		var e entity
		value := raw
		if replace[0] != "" {
			value = strings.ReplaceAll(raw, replace[0], replace[1])
		}
		if err := json.Unmarshal([]byte(value), &e); err != nil {
			t.Fatal(err)
		}
		_, ok := resolve(facts.Movie{ID: 603, Title: "Матрица", Year: 1999}, tmdb.FactExternalIDs{Wikidata: "Q123", IMDb: "tt0133093"}, e)
		if ok != (replace[0] == "") {
			t.Fatal("identity gate", replace, ok)
		}
	}
}
func TestExtractNativeTextAndRevisionIdentity(t *testing.T) {
	raw := []byte(`<script>{"wgRevisionId":123,"wgWikibaseItemId":"Q123"}</script><h2>Сюжет</h2><p>Этот текст сюжета не должен попадать в подборку историй.</p><h2>Создание фильма</h2><h3>Звуковые эффекты</h3><p>Для создания звука команда разбивала арбузы перед микрофоном.<sup>[12]</sup><style>.css {display:none}</style></p><h2>Критика</h2><p>Этот текст рецензии тоже не должен попасть в подборку.</p>`)
	movie := facts.Movie{ID: 1, Title: "Фильм", Year: 2000}
	episodes, err := extract(raw, movie, "Q123")
	if err != nil || len(episodes) != 1 {
		t.Fatal(episodes, err)
	}
	if episodes[0].Text != "Для создания звука команда разбивала арбузы перед микрофоном." || episodes[0].RevisionID != 123 {
		t.Fatal(episodes)
	}
	_, err = extract(raw, movie, "Q999")
	if err == nil {
		t.Fatal("wrong article accepted")
	}
	changed := []byte(strings.ReplaceAll(string(raw), "123,", "124,"))
	again, err := extract(changed, movie, "Q123")
	if err != nil || again[0].Key != episodes[0].Key {
		t.Fatal("revision edit changed story key", err)
	}
}
func TestLongParagraphWindowsAreContiguousAndComplete(t *testing.T) {
	text := strings.Repeat("Художники построили для съёмок сложную декорацию в павильоне. ", 24)
	windows := episodeWindows(strings.TrimSpace(text))
	if len(windows) == 0 {
		t.Fatal("long paragraph has no windows")
	}
	for _, window := range windows {
		if !strings.Contains(text, window) || !eligible(window) {
			t.Fatal("not a source window", window)
		}
	}
}

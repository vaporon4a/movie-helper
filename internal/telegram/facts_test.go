package telegram

import (
	"github.com/vaporon4a/movie-helper/internal/daily"
	"strings"
	"testing"
)

func TestExtractiveFactFormatsMovieAndHonestAttribution(t *testing.T) {
	item := daily.Item{Text: "Предпросмотр · «Фильм & кино» (2000)\n\nХудожники сделали <макет>.\n\n" + daily.WikipediaExtractiveAttribution, Source: "https://ru.wikipedia.org/w/index.php?oldid=123"}
	message := factMessage(item)
	for _, expected := range []string{"<b>«Фильм &amp; кино» (2000)</b>", "&lt;макет&gt;", "Исходный русский текст", "oldid=123", "CC BY-SA 4.0"} {
		if !strings.Contains(message, expected) {
			t.Fatal(message, expected)
		}
	}
	if strings.Contains(message, "переработан AI") {
		t.Fatal("false rewrite attribution", message)
	}
}

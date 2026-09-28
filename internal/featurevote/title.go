package featurevote

import (
	"strings"
	"unicode"
)

func FallbackTitle(text string) string {
	text = strings.TrimSpace(text)
	if cut := strings.IndexAny(text, ".!?\n"); cut >= 0 {
		text = strings.TrimSpace(text[:cut])
	}
	words := strings.Fields(text)
	if len(words) > 8 {
		words = words[:8]
	}
	title := strings.Join(words, " ")
	runes := []rune(title)
	if len(runes) > 60 {
		runes = runes[:59]
		title = strings.TrimRightFunc(string(runes), unicode.IsSpace) + "…"
	}
	if title == "" {
		return "Идея без названия"
	}
	return title
}

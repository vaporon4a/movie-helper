package featurevote

import "testing"

func TestFallbackTitleIsUnicodeSafeAndBounded(t *testing.T) {
	got := FallbackTitle("  Очень длинная идея о том, чтобы добавить удобный список просмотренных фильмов каждому участнику. Подробности")
	if got != "Очень длинная идея о том, чтобы добавить удобный" {
		t.Fatalf("title=%q", got)
	}
	if got := FallbackTitle("!!!"); got != "Идея без названия" {
		t.Fatalf("empty=%q", got)
	}
}

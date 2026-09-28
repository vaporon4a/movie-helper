package ai

import (
	"context"
	"errors"
	"testing"
)

type titleGeneratorStub struct {
	result Selection
	err    error
}

func (s titleGeneratorStub) Generate(context.Context, string, []Part) (Selection, error) {
	return s.result, s.err
}

func TestFeatureTitleGeneratorValidatesAndFallsBack(t *testing.T) {
	zero := 0
	valid := FeatureTitleGenerator{Primary: titleGeneratorStub{result: Selection{Index: &zero, Text: "Добавить общий список просмотренных фильмов"}}}
	if got := valid.Title(context.Background(), "Хотим список"); got != "Добавить общий список просмотренных фильмов" {
		t.Fatalf("title=%q", got)
	}
	invalid := FeatureTitleGenerator{Primary: titleGeneratorStub{result: Selection{Index: &zero, Text: "Список"}}, Secondary: titleGeneratorStub{err: errors.New("offline")}}
	if got := invalid.Title(context.Background(), "Добавить голосовые напоминания о начале киновечера. Остальной текст"); got != "Добавить голосовые напоминания о начале киновечера" {
		t.Fatalf("fallback=%q", got)
	}
}

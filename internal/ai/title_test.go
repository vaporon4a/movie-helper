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

func TestFeatureTitleGeneratorValidatesResult(t *testing.T) {
	zero := 0
	valid := FeatureTitleGenerator{Primary: titleGeneratorStub{result: Selection{Index: &zero, Text: "Добавить общий список просмотренных фильмов"}}}
	if got, ok := valid.TryTitle(context.Background(), "Хотим список"); !ok || got != "Добавить общий список просмотренных фильмов" {
		t.Fatalf("title=%q ok=%v", got, ok)
	}
	invalid := FeatureTitleGenerator{Primary: titleGeneratorStub{result: Selection{Index: &zero, Text: "Список"}}, Secondary: titleGeneratorStub{err: errors.New("offline")}}
	if got, ok := invalid.TryTitle(context.Background(), "Добавить голосовые напоминания о начале киновечера. Остальной текст"); ok || got != "" {
		t.Fatalf("title=%q ok=%v", got, ok)
	}
}

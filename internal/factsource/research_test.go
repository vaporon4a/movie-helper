package factsource

import (
	"strings"
	"testing"
	"time"
)

func TestResearchRussianSourcesAreBoundedAndRevisionBound(t *testing.T) {
	paragraph := strings.Repeat("Достаточно длинное описание начала фильма. ", 4)
	raw := []byte(`<script>{"wgRevisionId":123,"wgWikibaseItemId":"Q123"}</script><h2>Сюжет</h2><p>` + paragraph + `</p><p>` + paragraph + ` Вторая часть.</p><p>РАЗВЯЗКА ` + paragraph + `</p><h2>Критика</h2><p>` + paragraph + `</p>`)
	out, err := researchExtract(raw, "Q123", 603, time.Now())
	if err != nil || len(out) != 3 {
		t.Fatal(out, err)
	}
	if out[2].Kind != "opinion" || out[0].Revision != "123" {
		t.Fatal(out)
	}
	for _, source := range out {
		if strings.Contains(source.Text, "РАЗВЯЗКА") {
			t.Fatal("late plot included")
		}
		if err = source.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = researchExtract(raw, "Q124", 603, time.Now()); err == nil {
		t.Fatal("identity bypass")
	}
}

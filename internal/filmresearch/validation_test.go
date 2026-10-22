package filmresearch

import (
	"errors"
	"testing"
	"time"
)

func TestSourceSnapshotChecksOffsetsIdentityAndFormat(t *testing.T) {
	valid := SourceSnapshot{Version: SourceV1, TMDBID: 603, Kind: "plot", URL: "https://example.org/source?oldid=1", Language: "ru", Attribution: "fixture", Text: "Текст", Spans: []EvidenceSpan{{ID: "p1", Start: 0, End: len("Текст")}}, FetchedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*SourceSnapshot){
		"version":         func(s *SourceSnapshot) { s.Version = "future" },
		"identity":        func(s *SourceSnapshot) { s.TMDBID = 0 },
		"source type":     func(s *SourceSnapshot) { s.Kind = "trusted_ai_fact" },
		"credentials":     func(s *SourceSnapshot) { s.URL = "https://user:secret@example.org" },
		"non-https":       func(s *SourceSnapshot) { s.URL = "file:///etc/passwd" },
		"empty":           func(s *SourceSnapshot) { s.Text = " " },
		"missing spans":   func(s *SourceSnapshot) { s.Spans = nil },
		"duplicate spans": func(s *SourceSnapshot) { s.Spans = append(s.Spans, s.Spans[0]) },
		"negative offset": func(s *SourceSnapshot) { s.Spans[0].Start = -1 },
		"past end":        func(s *SourceSnapshot) { s.Spans[0].End++ },
		"split UTF8":      func(s *SourceSnapshot) { s.Spans[0].Start = 1 },
	} {
		t.Run(name, func(t *testing.T) {
			s := valid
			s.Spans = append([]EvidenceSpan(nil), valid.Spans...)
			mutate(&s)
			if err := s.Validate(); !errors.Is(err, ErrInvalid) {
				t.Fatal(err)
			}
		})
	}
	a, err := valid.ID()
	if err != nil {
		t.Fatal(err)
	}
	valid.Text = "текст"
	b, err := valid.ID()
	if err != nil || a == b {
		t.Fatal("content change did not change identity", err)
	}
}

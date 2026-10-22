package filmresearch

import (
	"testing"

	"github.com/vaporon4a/movie-helper/internal/clubprofile"
)

func TestEvidenceAliasesCannotSelectMissingSource(t *testing.T) {
	b := Bundle{Dossier: Dossier{SourceIDs: []string{"immutable-hash"}}}
	for _, id := range []string{"s0", "s2", "s01", "1", "immutable-hash", "s-1"} {
		_, err := ResolveReferences(b, Proposal{Questions: []Candidate{{Evidence: []clubprofile.EvidenceReference{{SourceID: id, SpanID: "text"}}}}})
		if err == nil {
			t.Fatalf("accepted %s", id)
		}
	}
	p, err := ResolveReferences(b, Proposal{Questions: []Candidate{{Evidence: []clubprofile.EvidenceReference{{SourceID: "s1", SpanID: "text"}}}}})
	if err != nil || p.Questions[0].Evidence[0].SourceID != "immutable-hash" {
		t.Fatal(p, err)
	}
	if _, err = GenerationInput(b, Audit{}); err == nil {
		t.Fatal("malformed source index")
	}
}
func TestAuditRequiresFourIndependentCompleteChecks(t *testing.T) {
	a := Audit{}
	for i := range 4 {
		a.Checks = append(a.Checks, Check{Index: i, Rationale: "Проверка", Supported: true, Neutral: true, SingleConstruct: true, SpoilerFree: true, Clear: true})
	}
	if !a.Accepted() {
		t.Fatal("valid audit rejected")
	}
	a.Checks[3].Index = 2
	if a.Accepted() {
		t.Fatal("duplicate check")
	}
	a.Checks[3].Index = 3
	a.Checks[3].SpoilerFree = false
	if a.Accepted() {
		t.Fatal("spoiler accepted")
	}
}

func TestJudgeQuoteMustBelongToCitedSpan(t *testing.T) {
	quote := "Это точная цитата из источника о фильме."
	source := SourceSnapshot{Text: quote + " Чужое продолжение.", Spans: []EvidenceSpan{{ID: "p1", End: len(quote)}}}
	b := Bundle{Dossier: Dossier{SourceIDs: []string{"source"}}, Sources: []SourceSnapshot{source}}
	a := Audit{}
	for i := range 4 {
		b.Preparation.Questions = append(b.Preparation.Questions, clubprofile.Question{Measurement: &clubprofile.MeasurementDescriptor{Evidence: []clubprofile.EvidenceReference{{SourceID: "source", SpanID: "p1"}}}})
		a.Checks = append(a.Checks, Check{Index: i, Quote: quote, Rationale: "Предмет подтверждён цитатой.", Supported: true, Neutral: true, SingleConstruct: true, SpoilerFree: true, Clear: true})
	}
	if !a.GroundedAccepted(b) {
		t.Fatal("valid quote rejected")
	}
	for _, invalid := range []string{"Это выдуманная цитата, которой нет в исходнике.", quote + " Чужое продолжение.", "Коротко"} {
		a.Checks[0].Quote = invalid
		if a.GroundedAccepted(b) {
			t.Fatal("unsupported quote accepted", invalid)
		}
	}
	a.Checks[0].Quote = quote
	b.Preparation.Questions[0].Measurement.Evidence[0].SpanID = "missing"
	if a.GroundedAccepted(b) {
		t.Fatal("foreign span accepted")
	}
}

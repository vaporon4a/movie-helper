package clubprofile

import (
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
)

func pilotQuestions(t *testing.T) []Question {
	t.Helper()
	raw, err := os.ReadFile("testdata/pilot_g.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Question string
		Film     struct {
			TMDBID int64 `json:"tmdb_id"`
		}
		Descriptor struct {
			MeasurementDescriptor
			Scale string `json:"scale_id"`
		}
		Options []struct {
			Key, Label  string
			Substantive bool
		}
	}
	if err = json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	questions := make([]Question, 0, len(fixtures))
	for _, fixture := range fixtures {
		d := fixture.Descriptor.MeasurementDescriptor
		d.TMDBID, d.Context, d.DossierID = fixture.Film.TMDBID, fixture.Question, "dossier-fixture"
		d.Evidence = []EvidenceReference{{SourceID: "source-fixture", SpanID: "plot"}}
		q := Question{Topic: PostWatch, Version: QuestionV2, Intent: d.MeasurementID, Scale: fixture.Descriptor.Scale, Text: fixture.Question, Anonymous: true, Revoting: true, Source: "film_research", Measurement: &d}
		for _, option := range fixture.Options {
			q.Options = append(q.Options, Option{Key: option.Key, Text: option.Label, Substantive: option.Substantive})
		}
		questions = append(questions, q)
	}
	return questions
}

func TestMeasurementPilotContractAndPublicationGate(t *testing.T) {
	questions := pilotQuestions(t)
	if len(questions) != 8 {
		t.Fatal(len(questions))
	}
	for _, q := range questions {
		if err := q.ValidateCandidate(); err != nil {
			t.Fatal(q.Text, err)
		}
		if err := q.Validate(); !errors.Is(err, ErrUnapprovedMeasurement) {
			t.Fatal("draft entered delivery", err)
		}
		raw, err := json.Marshal(q)
		if err != nil {
			t.Fatal(err)
		}
		var restored Question
		if err = json.Unmarshal(raw, &restored); err != nil {
			t.Fatal(err)
		}
		if err = restored.ValidateCandidate(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMeasurementRejectsContractMutation(t *testing.T) {
	for name, mutate := range map[string]func(*Question){
		"unknown version":    func(q *Question) { q.Version = "future" },
		"unknown family":     func(q *Question) { q.Measurement.FamilyID = "made-up" },
		"wrong registry":     func(q *Question) { q.Measurement.RegistryVersion = "future" },
		"wrong target":       func(q *Question) { q.Measurement.TargetID = "world.technology" },
		"wrong measurement":  func(q *Question) { q.Measurement.MeasurementID = "enjoyment" },
		"wrong intent":       func(q *Question) { q.Intent = Enjoyment },
		"wrong scale":        func(q *Question) { q.Scale = "credibility-5-v1" },
		"no descriptor":      func(q *Question) { q.Measurement = nil },
		"old aspect":         func(q *Question) { q.Aspect = Characters },
		"wrong order":        func(q *Question) { slices.Reverse(q.Options) },
		"changed label":      func(q *Question) { q.Options[0].Text = "Нравится" },
		"changed key":        func(q *Question) { q.Options[0].Key = "custom" },
		"skip counted":       func(q *Question) { q.Options[6].Substantive = true },
		"provider metadata":  func(q *Question) { q.Options[0].ProviderID = 4 },
		"no evidence":        func(q *Question) { q.Measurement.Evidence = nil },
		"duplicate evidence": func(q *Question) { q.Measurement.Evidence = append(q.Measurement.Evidence, q.Measurement.Evidence[0]) },
		"long wording":       func(q *Question) { q.Text = strings.Repeat("я", 161) },
	} {
		t.Run(name, func(t *testing.T) {
			q := pilotQuestions(t)[0]
			mutate(&q)
			if err := q.ValidateCandidate(); !errors.Is(err, ErrInvalid) {
				t.Fatal(err)
			}
		})
	}
}

func TestMeasurementRegistryRelationsAndIndependentOptions(t *testing.T) {
	r, err := loadMeasurementRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Targets) != 18 || len(r.Scales) != 4 || len(r.Families) != 8 {
		t.Fatal("registry size changed")
	}
	seen := map[string]bool{}
	for _, f := range r.Families {
		if seen[f.ID] {
			t.Fatal("duplicate family")
		}
		seen[f.ID] = true
		i := slices.IndexFunc(r.Targets, func(target measurementTarget) bool { return target.ID == f.Target })
		if i < 0 || !slices.Contains(r.Targets[i].Allowed, f.Measurement) {
			t.Fatal(f)
		}
		j := slices.IndexFunc(r.Scales, func(scale measurementScale) bool { return scale.ID == f.Scale })
		if j < 0 || r.Scales[j].Measurement != f.Measurement {
			t.Fatal(f)
		}
	}
	a, err := MeasurementOptions("interest-5-v1")
	if err != nil {
		t.Fatal(err)
	}
	a[0].Text = "mutated"
	b, err := MeasurementOptions("interest-5-v1")
	if err != nil || b[0].Text == a[0].Text {
		t.Fatal("shared options", err)
	}
	if _, err = MeasurementOptions("missing"); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}

func TestMeasurementDoesNotReinterpretV1(t *testing.T) {
	q, err := ReviewQuestion(Overall, "Матрица")
	if err != nil {
		t.Fatal(err)
	}
	if q.Options[0].Key != "overall:1" || q.Options[0].Text != "Совсем не понравился" || q.Options[5].Key != "not_watched" {
		t.Fatal(q.Options)
	}
	if err = q.Validate(); err != nil {
		t.Fatal(err)
	}
	q.Measurement = pilotQuestions(t)[0].Measurement
	if err = q.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatal("mixed versions", err)
	}
}

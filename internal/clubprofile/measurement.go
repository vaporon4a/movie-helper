package clubprofile

import (
	_ "embed"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"unicode/utf8"
)

// Draft families can be inspected and stored, but must never enter delivery.
var ErrUnapprovedMeasurement = errors.New("measurement family is not approved for publication")

const RegistryV2 = "comparison-v2"

const WordingV2 = "grounded-single-construct-v2"

const WordingV1 = "grounded-single-construct-v1"

type EvidenceReference struct {
	SourceID string `json:"source_id"`
	SpanID   string `json:"span_id"`
}

// MeasurementDescriptor is part of the immutable question snapshot. Names of
// characters belong in Context, not in the analytical target or family codes.
type MeasurementDescriptor struct {
	TargetID        string              `json:"target_id"`
	MeasurementID   string              `json:"measurement_id"`
	FamilyID        string              `json:"comparison_family_id"`
	TaxonomyVersion string              `json:"taxonomy_version"`
	RegistryVersion string              `json:"comparison_registry_version"`
	WordingVersion  string              `json:"wording_policy_version"`
	WatchRevision   int                 `json:"watch_revision,omitempty"`
	TMDBID          int64               `json:"tmdb_id"`
	Context         string              `json:"context"`
	DossierID       string              `json:"dossier_id"`
	Evidence        []EvidenceReference `json:"evidence"`
}

type measurementTarget struct {
	ID      string   `json:"id"`
	Allowed []string `json:"allowed_measurements"`
}

type measurementScale struct {
	ID          string `json:"id"`
	Measurement string `json:"measurement_id"`
	Options     []struct {
		Key         string `json:"key"`
		Label       string `json:"label"`
		Definition  string `json:"definition"`
		Substantive bool   `json:"substantive"`
	} `json:"options"`
}

type measurementFamily struct {
	Label       string `json:"label"`
	Definition  string `json:"definition"`
	ID          string `json:"id"`
	Target      string `json:"target_id"`
	Measurement string `json:"measurement_id"`
	Scale       string `json:"scale_id"`
	Status      string `json:"status"`
}

type measurementRegistry struct {
	TaxonomyVersion string              `json:"taxonomy_version"`
	Version         string              `json:"comparison_registry_version"`
	Targets         []measurementTarget `json:"targets"`
	Scales          []measurementScale  `json:"scales"`
	Families        []measurementFamily `json:"comparison_families"`
}

//go:embed measurement_registry.json
var measurementRegistryJSON []byte

//go:embed measurement_registry_v2.json
var measurementRegistryV2JSON []byte

// Decode on demand: no mutable exported registry, panic at init, or hidden I/O.
func loadMeasurementRegistry() (measurementRegistry, error) {
	var r measurementRegistry
	err := json.Unmarshal(measurementRegistryJSON, &r)
	return r, err
}

// MeasurementOptions returns independent canonical options, in their fixed order.
func MeasurementOptions(scaleID string) ([]Option, error) {
	r, err := loadMeasurementRegistry()
	if err != nil {
		return nil, err
	}
	for _, scale := range r.Scales {
		if scale.ID == scaleID {
			out := make([]Option, len(scale.Options))
			for i, option := range scale.Options {
				out[i] = Option{Key: option.Key, Text: option.Label, Substantive: option.Substantive}
			}
			return out, nil
		}
	}
	return nil, ErrInvalid
}

// ValidateCandidate checks the typed contract, not the truth or neutrality of
// natural language. Delivery must call Validate, which additionally gates drafts.
func (q Question) ValidateCandidate() error {
	if q.Version != QuestionV2 {
		return ErrInvalid
	}
	if err := q.validateShape(); err != nil {
		return err
	}
	return q.validateMeasurement(false)
}

func (q Question) validateMeasurement(requireApproved bool) error {
	d := q.Measurement
	if d == nil || q.Topic != PostWatch || q.Aspect != "" || !q.Revoting || q.Source != "film_research" || utf8.RuneCountInString(q.Text) > 160 {
		return ErrInvalid
	}
	if d.TMDBID <= 0 || strings.TrimSpace(d.Context) == "" || utf8.RuneCountInString(d.Context) > 300 || !slices.Contains([]string{WordingV1, WordingV2, "pilot-g-original"}, d.WordingVersion) || !validEvidence(d) {
		return ErrInvalid
	}
	r, err := registryFor(d.RegistryVersion)
	if err != nil {
		return err
	}
	if d.RegistryVersion == RegistryV2 && (d.WatchRevision < 1 || d.WordingVersion != WordingV2 || !measurementWording(q) || !measurementTopic(q)) {
		return ErrInvalid
	}
	if d.TaxonomyVersion != r.TaxonomyVersion || d.RegistryVersion != r.Version {
		return ErrInvalid
	}
	for _, family := range r.Families {
		if family.ID == d.FamilyID {
			return q.validateFamily(family, requireApproved)
		}
	}
	return ErrInvalid
}

func (q Question) validateFamily(f measurementFamily, requireApproved bool) error {
	d := q.Measurement
	if d.TargetID != f.Target || d.MeasurementID != f.Measurement || q.Scale != f.Scale || q.Intent != f.Measurement {
		return ErrInvalid
	}
	options, err := MeasurementOptions(q.Scale)
	if err != nil {
		return err
	}
	if !slices.Equal(q.Options, options) {
		return ErrInvalid
	}
	if requireApproved && f.Status != "approved" {
		return ErrUnapprovedMeasurement
	}
	return nil
}

func validEvidence(d *MeasurementDescriptor) bool {
	if d.DossierID == "" || len(d.Evidence) == 0 || len(d.Evidence) > 3 {
		return false
	}
	seen := make(map[EvidenceReference]bool, len(d.Evidence))
	for _, ref := range d.Evidence {
		if ref.SourceID == "" || ref.SpanID == "" || seen[ref] {
			return false
		}
		seen[ref] = true
	}
	return true
}

func registryFor(version string) (measurementRegistry, error) {
	if version != RegistryV2 {
		return loadMeasurementRegistry()
	}
	var r measurementRegistry
	err := json.Unmarshal(measurementRegistryV2JSON, &r)
	return r, err
}

// ResearchFamily is the bounded vocabulary given to the generator. Families
// describe a construct, not a claim that every movie has that characteristic.
type ResearchFamily struct {
	Definition  string `json:"definition"`
	ID          string `json:"id"`
	Target      string `json:"target"`
	Measurement string `json:"measurement"`
	Scale       string `json:"scale"`
}

func ResearchFamilies() ([]ResearchFamily, error) {
	r, err := registryFor(RegistryV2)
	if err != nil {
		return nil, err
	}
	out := make([]ResearchFamily, 0, len(r.Families))
	for _, f := range r.Families {
		out = append(out, ResearchFamily{ID: f.ID, Target: f.Target, Measurement: f.Measurement, Scale: f.Scale, Definition: f.Definition})
	}
	return out, nil
}
func ResearchQuestion(family, text, context string, tmdb int64, revision int, dossier string, evidence []EvidenceReference) (Question, error) {
	r, err := registryFor(RegistryV2)
	if err != nil {
		return Question{}, err
	}
	for _, f := range r.Families {
		if f.ID != family {
			continue
		}
		options, e := MeasurementOptions(f.Scale)
		if e != nil {
			return Question{}, e
		}
		q := Question{Version: QuestionV2, Topic: PostWatch, Intent: f.Measurement, Scale: f.Scale, Text: text, Options: options, Anonymous: true, Revoting: true, Source: "film_research",
			Measurement: &MeasurementDescriptor{TargetID: f.Target, MeasurementID: f.Measurement, FamilyID: f.ID, TaxonomyVersion: r.TaxonomyVersion, RegistryVersion: r.Version, WordingVersion: WordingV2, TMDBID: tmdb, WatchRevision: revision, Context: context, DossierID: dossier, Evidence: evidence}}
		return q, q.Validate()
	}
	return Question{}, ErrInvalid
}
func (q Question) PlanTarget() Aspect {
	if q.Version == QuestionV2 && q.Measurement != nil {
		return Aspect(q.Measurement.TargetID)
	}
	return q.Aspect
}

func ResearchFamilyLabel(id string) string {
	r, err := registryFor(RegistryV2)
	if err != nil {
		return "Неизвестное измерение"
	}
	for _, f := range r.Families {
		if f.ID == id {
			return f.Label
		}
	}
	return "Неизвестное измерение"
}

// This conservative check catches obvious scale substitutions; it is not a
// substitute for the separate source/meaning audit.
func measurementWording(q Question) bool {
	text := strings.ToLower(q.Text)
	interest := strings.Contains(text, "интерес")
	enjoyment := strings.Contains(text, "нрав") || strings.Contains(text, "удовольств")
	credibility := strings.Contains(text, "правдоподоб") || strings.Contains(text, "убедител") || strings.Contains(text, "достовер")
	switch q.Measurement.MeasurementID {
	case "interest":
		return interest && !enjoyment && !credibility
	case "enjoyment":
		return enjoyment && !interest && !credibility
	case "credibility":
		return credibility && !interest && !enjoyment
	case "pace_preference":
		return strings.Contains(text, "темп")
	default:
		return false
	}
}

func measurementTopic(q Question) bool {
	text := strings.ToLower(q.Text)
	if q.Measurement.TargetID == "themes.loneliness" {
		return strings.Contains(text, "одиночеств")
	}
	if q.Measurement.TargetID == "world.technology" {
		for _, word := range []string{"съём", "съем", "операторск", "монтаж", "flo-mo", "bullet time"} {
			if strings.Contains(text, word) {
				return false
			}
		}
	}
	return true
}

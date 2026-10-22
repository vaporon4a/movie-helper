package filmresearch

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/vaporon4a/movie-helper/internal/clubprofile"
)

// Human-size aliases make evidence selection reliable. Only the adapter sees
// s1/s2; saved question descriptors always contain immutable content hashes.
type InputSource struct {
	ID, Kind, Language, Text string
	Spans                    []EvidenceSpan
}
type InputQuestion struct {
	Text, Family, Target, Measurement, Context string
	Options                                    []clubprofile.Option
	Evidence                                   []clubprofile.EvidenceReference
}
type Input struct {
	Title         string
	Year          int
	Sources       []InputSource
	Families      []clubprofile.ResearchFamily
	Questions     []InputQuestion
	PreviousAudit Audit
}

func GenerationInput(b Bundle, previous Audit) (Input, error) {
	if len(b.Sources) != len(b.Dossier.SourceIDs) {
		return Input{}, ErrInvalid
	}
	families, err := clubprofile.ResearchFamilies()
	if err != nil {
		return Input{}, err
	}
	in := Input{Title: b.Dossier.Title, Year: b.Dossier.Year, Families: families, PreviousAudit: previous}
	aliases := map[string]string{}
	for i, source := range b.Sources {
		alias := fmt.Sprintf("s%d", i+1)
		aliases[b.Dossier.SourceIDs[i]] = alias
		in.Sources = append(in.Sources, InputSource{ID: alias, Kind: source.Kind, Language: source.Language, Text: source.Text, Spans: source.Spans})
	}
	for _, q := range b.Preparation.Questions {
		d := q.Measurement
		if d == nil {
			return Input{}, ErrInvalid
		}
		candidate := InputQuestion{Text: q.Text, Family: d.FamilyID, Target: d.TargetID, Measurement: d.MeasurementID, Context: d.Context, Options: q.Options}
		for _, ref := range d.Evidence {
			candidate.Evidence = append(candidate.Evidence, clubprofile.EvidenceReference{SourceID: aliases[ref.SourceID], SpanID: ref.SpanID})
		}
		in.Questions = append(in.Questions, candidate)
	}
	return in, nil
}
func ResolveReferences(b Bundle, p Proposal) (Proposal, error) {
	for i := range p.Questions {
		for j, ref := range p.Questions[i].Evidence {
			index, err := strconv.Atoi(strings.TrimPrefix(ref.SourceID, "s"))
			if err != nil || index < 1 || index > len(b.Dossier.SourceIDs) || ref.SourceID != fmt.Sprintf("s%d", index) {
				return Proposal{}, ErrInvalid
			}
			p.Questions[i].Evidence[j].SourceID = b.Dossier.SourceIDs[index-1]
		}
	}
	return p, nil
}

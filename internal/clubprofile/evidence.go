package clubprofile

import (
	"cmp"
	"maps"
	"slices"
)

type ResearchEvidence struct {
	ChatID, PollID, WatchID, TMDBID        int64
	WatchRevision                          int
	Film, Date, Family, Measurement, Scale string
	Answers                                int
	Options                                []EvidenceOption
}
type EvidenceOption struct {
	Key, Text string
	Count     int
}
type ResearchExclusion struct {
	PollID int64
	Reason string
}

// Called on the very same canonical selection as the summary, without reweighting.
func researchEvidence(perFilm map[string]researchObservation) []ResearchEvidence {
	out := make([]ResearchEvidence, 0, len(perFilm))
	for _, o := range slices.Collect(maps.Values(perFilm)) {
		p, w := o.Poll, o.Watch
		d := p.Question.Measurement
		e := ResearchEvidence{ChatID: p.ChatID, PollID: p.ID, WatchID: w.ID, TMDBID: w.TMDBID, WatchRevision: w.Revision, Film: w.Film.Label(), Date: w.Date, Family: d.FamilyID, Measurement: d.MeasurementID, Scale: p.Question.Scale, Answers: (Observation{Question: p.Question, Result: *p.Result}).Answers()}
		for i, v := range p.Question.Options {
			if v.Substantive {
				e.Options = append(e.Options, EvidenceOption{v.Key, v.Text, p.Result.Counts[i]})
			}
		}
		out = append(out, e)
	}
	slices.SortFunc(out, func(a, b ResearchEvidence) int { return cmp.Compare(a.PollID, b.PollID) })
	return out
}
func (s ResearchFamilySummary) Divided() bool {
	if s.Films < 3 {
		return false
	}
	options, err := MeasurementOptions(s.Scale)
	if err != nil {
		return false
	}
	var substantive []Option
	for _, o := range options {
		if o.Substantive {
			substantive = append(substantive, o)
		}
	}
	if len(substantive) < 4 {
		return false
	}
	negative := s.Shares[substantive[0].Key] + s.Shares[substantive[1].Key]
	n := len(substantive)
	positive := s.Shares[substantive[n-1].Key] + s.Shares[substantive[n-2].Key]
	return negative >= 0.25 && positive >= 0.25
}

package clubprofile

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
)

const ResearchProjectionV1 = "research-projection-v1"

type ResearchSummary struct {
	Version, Digest          string
	Families                 []ResearchFamilySummary
	Provisional, Quarantined int
	CanonicalPollIDs         []int64
}
type ResearchFamilySummary struct {
	ChatID                             int64
	Family, Target, Measurement, Scale string
	Films, Answers                     int
	Shares                             map[string]float64
	Labels                             map[string]string
}
type researchObservation struct {
	Poll  Poll
	Watch WatchEvent
}

// ProjectResearch selects the first qualifying answer per viewing/family, then
// the latest qualifying viewing per movie/family. Repeats never add turnout or
// overweight a film. Different measurement families remain separate.
func ProjectResearch(watches []WatchEvent, sessions []ReviewSession, polls []Poll) ResearchSummary {
	result := ResearchSummary{Version: ResearchProjectionV1}
	byWatch := map[int64]WatchEvent{}
	bySession := map[int64]ReviewSession{}
	for _, w := range watches {
		if w.State == "active" {
			byWatch[w.ID] = w
		}
	}
	for _, s := range sessions {
		bySession[s.ID] = s
	}
	perWatch := canonicalResearchViewings(byWatch, bySession, polls, &result)
	perFilm := latestResearchFilms(perWatch)
	groups, canonical := groupResearchFilms(perFilm)
	for _, f := range groups {
		for key := range f.Shares {
			f.Shares[key] /= float64(f.Films)
		}
		result.Families = append(result.Families, *f)
	}
	slices.SortFunc(result.Families, func(a, b ResearchFamilySummary) int {
		if a.ChatID != b.ChatID {
			return cmp.Compare(a.ChatID, b.ChatID)
		}
		return strings.Compare(a.Family, b.Family)
	})
	slices.SortFunc(canonical, func(a, b Poll) int { return cmp.Compare(a.ID, b.ID) })
	for _, p := range canonical {
		result.CanonicalPollIDs = append(result.CanonicalPollIDs, p.ID)
	}
	raw, err := json.Marshal(struct {
		Version string
		Polls   []researchDigestInput
	}{ResearchProjectionV1, researchDigestInputs(canonical)})
	if err != nil {
		result.Digest = "unavailable"
		return result
	}
	hash := sha256.Sum256(raw)
	result.Digest = hex.EncodeToString(hash[:])
	return result
}

func canonicalResearchViewings(byWatch map[int64]WatchEvent, bySession map[int64]ReviewSession, polls []Poll, diagnostics *ResearchSummary) map[string]researchObservation {
	perWatch := map[string]researchObservation{}
	for _, p := range polls {
		if p.Question.Version != QuestionV2 || p.State != Completed || p.Result == nil {
			continue
		}
		s, exists := bySession[p.SessionID]
		w, active := byWatch[s.WatchID]
		if !exists || !active || w.ChatID != p.ChatID || s.ChatID != p.ChatID {
			continue
		}
		d := p.Question.Measurement
		if !validResearchObservation(p, s, w) {
			diagnostics.Quarantined++
			continue
		}
		n := (Observation{Question: p.Question, Result: *p.Result}).Answers()
		if n < 3 {
			diagnostics.Provisional++
			continue
		}
		key := fmt.Sprintf("%d:%s", w.ID, d.FamilyID)
		old, ok := perWatch[key]
		if !ok || p.ClosesAt.Before(old.Poll.ClosesAt) || (p.ClosesAt.Equal(old.Poll.ClosesAt) && p.ID < old.Poll.ID) {
			perWatch[key] = researchObservation{p, w}
		}
	}
	return perWatch
}

func latestResearchFilms(perWatch map[string]researchObservation) map[string]researchObservation {
	perFilm := map[string]researchObservation{}
	for _, o := range perWatch {
		key := fmt.Sprintf("%d:%d:%s", o.Watch.ChatID, o.Watch.TMDBID, o.Poll.Question.Measurement.FamilyID)
		old, ok := perFilm[key]
		if !ok || o.Watch.Date > old.Watch.Date || (o.Watch.Date == old.Watch.Date && o.Watch.ID > old.Watch.ID) {
			perFilm[key] = o
		}
	}
	return perFilm
}

func groupResearchFilms(perFilm map[string]researchObservation) (map[string]*ResearchFamilySummary, []Poll) {
	groups := map[string]*ResearchFamilySummary{}
	canonical := make([]Poll, 0, len(perFilm))
	observations := slices.Collect(maps.Values(perFilm))
	slices.SortFunc(observations, func(a, b researchObservation) int { return cmp.Compare(a.Poll.ID, b.Poll.ID) })
	for _, o := range observations {
		p := o.Poll
		d := p.Question.Measurement
		key := fmt.Sprintf("%d:%s", p.ChatID, d.FamilyID)
		f := groups[key]
		if f == nil {
			f = &ResearchFamilySummary{ChatID: p.ChatID, Family: d.FamilyID, Target: d.TargetID, Measurement: d.MeasurementID, Scale: p.Question.Scale, Shares: map[string]float64{}, Labels: map[string]string{}}
			groups[key] = f
		}
		n := (Observation{Question: p.Question, Result: *p.Result}).Answers()
		f.Films++
		f.Answers += n
		for i, option := range p.Question.Options {
			if option.Substantive {
				f.Shares[option.Key] += float64(p.Result.Counts[i]) / float64(n)
				f.Labels[option.Key] = option.Text
			}
		}
		canonical = append(canonical, p)
	}
	return groups, canonical
}

func validResearchObservation(p Poll, s ReviewSession, w WatchEvent) bool {
	d := p.Question.Measurement
	return d != nil && p.Question.Validate() == nil && p.Result.ValidateFinal(p.Question, p.TelegramID) == nil && d.TMDBID == w.TMDBID && d.WatchRevision == w.Revision && s.Plan.compatibleResearch(Observation{Question: p.Question, Result: *p.Result})
}

// Only analytical inputs belong in the digest; Telegram cleanup and retries do not.
type researchDigestInput struct {
	ID, ChatID, SessionID int64
	Question              Question
	Counts                []int
	Total                 *int
}

func researchDigestInputs(polls []Poll) []researchDigestInput {
	inputs := make([]researchDigestInput, 0, len(polls))
	for _, p := range polls {
		q := p.Question
		q.Description = ""
		inputs = append(inputs, researchDigestInput{p.ID, p.ChatID, p.SessionID, q, p.Result.Counts, p.Result.Total})
	}
	return inputs
}

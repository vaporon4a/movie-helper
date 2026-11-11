package clubprofile

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"time"
)

const StatisticsVersion = "club-statistics-v1"

// StatisticsInput is read atomically. It contains aggregate answers, never voters.
type StatisticsInput struct {
	ChatID   int64
	Zone     string
	Watches  []WatchEvent
	Sessions []ReviewSession
	Polls    []Poll
}

type StatisticsPeriod struct{ Start, End time.Time }

func (p StatisticsPeriod) Contains(t time.Time) bool {
	return (p.Start.IsZero() || !t.Before(p.Start)) && (p.End.IsZero() || t.Before(p.End))
}

type StatisticsClaim struct {
	ID, Text             string
	PollID, FilmID       int64
	Priority             int
	ClosedAt, ReceivedAt time.Time
	Question             Question
	Counts               []int
	WatchRevision        int
}

type StatisticsSnapshot struct {
	Version                                                     string
	ChatID                                                      int64
	Zone                                                        string
	AsOf                                                        time.Time
	Period                                                      StatisticsPeriod
	Films, Polls, Answers, Ordinary, PostWatch, Legacy, Invalid int
	Claims                                                      []StatisticsClaim
	Research                                                    ResearchSummary
}

func BuildStatistics(in StatisticsInput, period StatisticsPeriod, now time.Time) StatisticsSnapshot {
	s := StatisticsSnapshot{Version: StatisticsVersion, ChatID: in.ChatID, Zone: in.Zone, AsOf: now, Period: period}
	if in.ChatID == 0 {
		return s
	}
	in = scopedStatistics(in, now)
	s.Films = ProjectProfile(in.Watches, in.Sessions, nil).Films
	s.Research = ProjectResearch(in.Watches, in.Sessions, in.Polls)
	canonical := canonicalStatistics(in, s.Research)
	for _, p := range in.Polls {
		if p.State != Completed || p.Result == nil || !period.Contains(p.ClosesAt) {
			continue
		}
		if !s.countPoll(p) {
			continue
		}
		if c, ok := canonical[p.ID]; ok && c.ID != "" {
			s.Claims = append(s.Claims, c)
		}
	}
	sortStatisticsClaims(s.Claims)
	return s
}

func (s *StatisticsSnapshot) countPoll(p Poll) bool {
	if p.Question.Intent == LegacyInterest || p.Result.Source == SourceLegacy {
		s.Legacy++
		return false
	}
	if !validStatisticsPoll(p) {
		s.Invalid++
		return false
	}
	s.Polls++
	s.Answers += *p.Result.Total
	if p.Question.Topic == PostWatch {
		s.PostWatch++
	} else {
		s.Ordinary++
	}
	return true
}

func scopedStatistics(in StatisticsInput, now time.Time) StatisticsInput {
	in.Watches = slices.DeleteFunc(slices.Clone(in.Watches), func(w WatchEvent) bool { return w.ChatID != in.ChatID || w.State != "active" })
	in.Sessions = slices.DeleteFunc(slices.Clone(in.Sessions), func(s ReviewSession) bool { return s.ChatID != in.ChatID })
	in.Polls = slices.DeleteFunc(slices.Clone(in.Polls), func(p Poll) bool {
		return p.ChatID != in.ChatID || p.ClosesAt.After(now) || (p.Result != nil && p.Result.ReceivedAt.After(now))
	})
	return in
}
func validStatisticsPoll(p Poll) bool {
	return p.State == Completed && p.Result != nil && p.Question.Validate() == nil && p.Result.ValidateFinal(p.Question, p.TelegramID) == nil
}

func canonicalStatistics(in StatisticsInput, research ResearchSummary) map[int64]StatisticsClaim {
	out := map[int64]StatisticsClaim{}
	byPoll := map[int64]Poll{}
	for _, p := range in.Polls {
		byPoll[p.ID] = p
	}
	for _, e := range research.Evidence {
		p := byPoll[e.PollID]
		text := fmt.Sprintf("🎞 %s · %s\n%s", statisticsClip(e.Film, 90), ResearchFamilyLabel(e.Family), statisticsDistribution(p))
		out[p.ID] = makeStatisticsClaim(p, e.TMDBID, e.WatchRevision, 2, text)
	}
	for _, o := range canonicalOverall(in) {
		p, w := o.Poll, o.Watch
		out[p.ID] = makeStatisticsClaim(p, w.TMDBID, w.Revision, 3, overallStatisticsText(p, w))
	}
	for _, p := range in.Polls {
		if p.Question.Topic != Genre && p.Question.Topic != Reference {
			continue
		}
		if validStatisticsPoll(p) && (Observation{Question: p.Question, Result: *p.Result}).Answers() >= 3 {
			out[p.ID] = makeStatisticsClaim(p, 0, 0, 1, choiceStatisticsText(p))
		}
	}
	return out
}

// First sufficient poll per viewing, then latest sufficient viewing per film.
// V1 does not encode a historical watch revision; no such revision is invented.
func canonicalOverall(in StatisticsInput) map[int64]researchObservation {
	watches := map[int64]WatchEvent{}
	for _, w := range in.Watches {
		if w.State == "active" {
			watches[w.ID] = w
		}
	}
	sessions := map[int64]int64{}
	for _, s := range in.Sessions {
		sessions[s.ID] = s.WatchID
	}
	first := map[int64]researchObservation{}
	for _, p := range in.Polls {
		w, ok := watches[sessions[p.SessionID]]
		if !ok || w.ChatID != p.ChatID || !validOverall(p) {
			continue
		}
		old, exists := first[w.ID]
		if !exists || p.ClosesAt.Before(old.Poll.ClosesAt) || (p.ClosesAt.Equal(old.Poll.ClosesAt) && p.ID < old.Poll.ID) {
			first[w.ID] = researchObservation{p, w}
		}
	}
	return latestOverallFilms(first)
}

func latestOverallFilms(first map[int64]researchObservation) map[int64]researchObservation {
	latest := map[int64]researchObservation{}
	for _, o := range first {
		old, ok := latest[o.Watch.TMDBID]
		if !ok || o.Watch.Date > old.Watch.Date || (o.Watch.Date == old.Watch.Date && o.Watch.ID > old.Watch.ID) {
			latest[o.Watch.TMDBID] = o
		}
	}
	return latest
}
func validOverall(p Poll) bool {
	if !validStatisticsPoll(p) || p.Question.Version != QuestionV1 || p.Question.Topic != PostWatch || p.Question.Aspect != Overall || p.Question.Intent != Enjoyment || p.Question.Scale != aspectScale(Overall) {
		return false
	}
	expected, err := ReviewQuestion(Overall, "film")
	if err != nil || len(p.Question.Options) != len(expected.Options) {
		return false
	}
	for i, o := range expected.Options {
		if p.Question.Options[i] != o {
			return false
		}
	}
	return (Observation{Question: p.Question, Result: *p.Result}).Answers() >= 3
}
func makeStatisticsClaim(p Poll, film int64, revision, priority int, text string) StatisticsClaim {
	c := StatisticsClaim{Text: text, PollID: p.ID, FilmID: film, Priority: priority, ClosedAt: p.ClosesAt, ReceivedAt: p.Result.ReceivedAt, Question: p.Question, Counts: slices.Clone(p.Result.Counts), WatchRevision: revision}
	question := p.Question
	question.Description = ""
	raw, err := json.Marshal(struct {
		Version  string
		Poll     int64
		Revision int
		Question Question
		Counts   []int
	}{StatisticsVersion, p.ID, revision, question, c.Counts})
	if err != nil {
		return c
	}
	hash := sha256.Sum256(raw)
	c.ID = hex.EncodeToString(hash[:])
	return c
}
func sortStatisticsClaims(claims []StatisticsClaim) {
	slices.SortFunc(claims, func(a, b StatisticsClaim) int {
		if c := b.ClosedAt.Compare(a.ClosedAt); c != 0 {
			return c
		}
		if c := cmp.Compare(b.Priority, a.Priority); c != 0 {
			return c
		}
		return cmp.Compare(a.PollID, b.PollID)
	})
}

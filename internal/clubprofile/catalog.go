package clubprofile

import (
	"context"
	"fmt"
	"math/rand/v2"
	"time"
)

const referenceHistoryWindow = 8 * 7 * 24 * time.Hour

type ProfileHistory interface {
	ProfilePolls(context.Context, int64, PollState) ([]Poll, error)
}

// ProfileQuestions reuses the curated catalog without recommendation calls or poster downloads.
type ProfileQuestions struct{ History ProfileHistory }

func (s ProfileQuestions) InterestQuestion(ctx context.Context, topic Topic, chat int64, seed uint64, now time.Time) (Question, error) {
	q := Question{Topic: topic, Intent: Interest, Scale: "single-interest-v1", Version: QuestionV1, Anonymous: true, Revoting: true, Source: "curated_catalog"}
	switch topic {
	case Genre:
		q.Text = "Какой из этих жанров вам ближе?"
		for _, o := range genreOptions(seed) {
			q.Options = append(q.Options, Option{Key: fmt.Sprintf("genre:%d", o.ProviderID), Text: o.Text, ProviderID: o.ProviderID, Substantive: true})
		}
	case Reference:
		q.Text = "Какой из этих фильмов вам нравится больше?"
		options, err := s.reference(ctx, chat, seed, now)
		if err != nil {
			return q, err
		}
		q.Options = options
		q.Options = append(q.Options, Option{Key: "unfamiliar", Text: "Не знаком(а) с этими фильмами / не могу выбрать"})
	case PostWatch:
		return q, ErrInvalid
	default:
		return q, ErrInvalid
	}
	return q, q.Validate()
}

func (s ProfileQuestions) reference(ctx context.Context, chat int64, seed uint64, now time.Time) ([]Option, error) {
	history, err := s.History.ProfilePolls(ctx, chat, "")
	if err != nil {
		return nil, err
	}
	recent := make(map[int64]bool)
	for _, p := range history {
		if p.Question.Topic != Reference || p.MessageID == 0 || p.OpenedAt.Before(now.Add(-referenceHistoryWindow)) {
			continue
		}
		for _, o := range p.Question.Options {
			recent[o.ProviderID] = true
		}
	}
	candidates := append([]referenceSeed(nil), referenceSeeds...)
	// #nosec G404 -- воспроизводимая ротация, не криптографический выбор.
	r := rand.New(rand.NewPCG(seed, seed^0x94d049bb133111eb))
	r.Shuffle(len(candidates), func(i, j int) { candidates[i], candidates[j] = candidates[j], candidates[i] })
	return profileReferenceOptions(candidates, recent), nil
}

type profileSeedPicker struct {
	options []Option
	used    map[int64]bool
	groups  map[string]bool
	decades map[int]bool
	recent  map[int64]bool
}

func profileReferenceOptions(seeds []referenceSeed, recent map[int64]bool) []Option {
	p := profileSeedPicker{options: make([]Option, 0, 8), used: make(map[int64]bool), groups: make(map[string]bool), decades: make(map[int]bool), recent: recent}
	for _, allowRecent := range []bool{false, true} {
		for _, diverse := range []bool{true, false} {
			p.add(seeds, allowRecent, diverse)
		}
	}
	return p.options
}

func (p *profileSeedPicker) add(seeds []referenceSeed, allowRecent, diverse bool) {
	for _, s := range seeds {
		if len(p.options) == 8 {
			return
		}
		if p.used[s.ID] || (!allowRecent && p.recent[s.ID]) || (diverse && (p.groups[s.Group] || p.decades[s.Decade])) {
			continue
		}
		p.options = append(p.options, Option{Key: fmt.Sprintf("movie:%d", s.ID), Text: s.Title, ProviderID: s.ID, Substantive: true})
		p.used[s.ID] = true
		p.groups[s.Group] = true
		p.decades[s.Decade] = true
	}
}

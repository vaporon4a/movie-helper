package clubprofile

import "slices"

type AspectSummary struct {
	Aspect                    Aspect
	Films, Questions, Answers int
	Shares                    map[string]float64
	Labels                    map[string]string
}

type ProfileSummary struct {
	Films, Viewings, RatedFilms, InterestPolls, LegacyPolls int
	Aspects                                                 []AspectSummary
}

type aspectFilm struct {
	sum                map[string]float64
	questions, answers int
}

// ProjectProfile gives each film equal weight, irrespective of repeat screenings or turnout.
func ProjectProfile(watches []WatchEvent, sessions []ReviewSession, polls []Poll) ProfileSummary {
	sessionFilms, filmCount, viewCount := profileFilmIndex(watches, sessions)
	result := ProfileSummary{Films: filmCount, Viewings: viewCount}
	rated := map[int64]bool{}
	aspects := map[Aspect]map[int64]*aspectFilm{}
	labels := map[Aspect]map[string]string{}
	result.InterestPolls, result.LegacyPolls = countInterestPolls(polls)
	for _, p := range polls {
		if p.State != Completed || p.Result == nil {
			continue
		}
		film := sessionFilms[p.SessionID]
		if film == 0 || !compatibleObservation(p) {
			continue
		}
		if p.Question.Aspect == Overall {
			rated[film] = true
			continue
		}
		accumulateAspect(aspects, labels, film, p)
	}
	result.RatedFilms = len(rated)
	for aspect, byFilm := range aspects {
		result.Aspects = append(result.Aspects, projectAspect(aspect, byFilm, labels[aspect]))
	}
	slices.SortFunc(result.Aspects, func(a, b AspectSummary) int {
		if a.Aspect < b.Aspect {
			return -1
		}
		if a.Aspect > b.Aspect {
			return 1
		}
		return 0
	})
	return result
}

func compatibleObservation(p Poll) bool {
	q := p.Question
	return q.Topic == PostWatch && q.Version == QuestionV1 && q.Scale == aspectScale(q.Aspect) && q.Intent == aspectIntent(q.Aspect) && (Observation{Question: q, Result: *p.Result}).Answers() > 0
}

func accumulateAspect(groups map[Aspect]map[int64]*aspectFilm, labels map[Aspect]map[string]string, film int64, p Poll) {
	a := p.Question.Aspect
	if groups[a] == nil {
		groups[a] = map[int64]*aspectFilm{}
		labels[a] = map[string]string{}
	}
	f := groups[a][film]
	if f == nil {
		f = &aspectFilm{sum: map[string]float64{}}
		groups[a][film] = f
	}
	n := (Observation{Question: p.Question, Result: *p.Result}).Answers()
	f.questions++
	f.answers += n
	for i, o := range p.Question.Options {
		if o.Substantive {
			f.sum[o.Key] += float64(p.Result.Counts[i]) / float64(n)
			labels[a][o.Key] = o.Text
		}
	}
}

func projectAspect(aspect Aspect, films map[int64]*aspectFilm, labels map[string]string) AspectSummary {
	s := AspectSummary{Aspect: aspect, Films: len(films), Shares: map[string]float64{}, Labels: labels}
	for _, f := range films {
		s.Questions += f.questions
		s.Answers += f.answers
		for key, sum := range f.sum {
			s.Shares[key] += sum / float64(f.questions) / float64(len(films))
		}
	}
	return s
}

func profileFilmIndex(watches []WatchEvent, sessions []ReviewSession) (map[int64]int64, int, int) {
	watchFilms := map[int64]int64{}
	films := map[int64]bool{}
	for _, w := range watches {
		if w.State == "active" {
			watchFilms[w.ID] = w.TMDBID
			films[w.TMDBID] = true
		}
	}
	sessionFilms := map[int64]int64{}
	for _, s := range sessions {
		if film := watchFilms[s.WatchID]; film != 0 {
			sessionFilms[s.ID] = film
		}
	}
	return sessionFilms, len(films), len(watchFilms)
}

func countInterestPolls(polls []Poll) (int, int) {
	interest, legacy := 0, 0
	for _, p := range polls {
		if p.State != Completed || p.Result == nil {
			continue
		}
		switch p.Question.Intent {
		case Interest:
			interest++
		case LegacyInterest:
			legacy++
		}
	}
	return interest, legacy
}

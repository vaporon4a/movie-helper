package clubprofile

import (
	"fmt"
	"unicode/utf16"
)

func DefaultReviewPlan() ReviewPlan {
	return ReviewPlan{Version: CoverageV1, Targets: []Aspect{Pace, Engagement, Characters, Visuals}, MinimumAnswers: 3, MaxQuestions: 7, MaxPerBatch: 2, MaxPerTarget: 2}
}

func (p ReviewPlan) Validate() error {
	if (p.Version != CoverageV1 && p.Version != CoverageV2) || len(p.Targets) != 4 || p.MinimumAnswers < 1 || p.MaxQuestions < 5 || p.MaxQuestions > 7 || p.MaxPerBatch < 1 || p.MaxPerBatch > 2 || p.MaxPerTarget != 2 {
		return ErrInvalid
	}
	if p.Version == CoverageV2 {
		return p.validateResearchTargets()
	}
	if p.PreparationID != "" || len(p.Questions) != 0 {
		return ErrInvalid
	}
	seen := map[Aspect]bool{}
	for _, a := range p.Targets {
		if seen[a] || a == Overall || aspectScale(a) == "" {
			return ErrInvalid
		}
		seen[a] = true
	}
	return nil
}

func (p ReviewPlan) Coverage(observations []Observation, unresolved bool) Coverage {
	c := Coverage{Covered: make(map[Aspect]bool), Reason: "collecting"}
	if p.Validate() != nil {
		c.Reason = "invalid_plan"
		return c
	}
	p.coverObservations(&c, observations)
	c.Sufficient = c.Overall && !unresolved
	for _, a := range p.Targets {
		c.Sufficient = c.Sufficient && c.Covered[a]
	}
	if c.Sufficient {
		c.Reason = "sufficient"
	} else if unresolved {
		c.Reason = "awaiting_final"
	}
	return c
}

func aspectIntent(a Aspect) string {
	if a == Overall {
		return Enjoyment
	}
	return AspectResponse
}

func aspectScale(a Aspect) string {
	switch a {
	case Overall:
		return "enjoyment-5-v1"
	case Pace:
		return "pace-3-v1"
	case Engagement:
		return "engagement-3-v1"
	case Characters:
		return "characters-3-v1"
	case Visuals:
		return "visuals-3-v1"
	default:
		return ""
	}
}

func ReviewQuestion(a Aspect, title string) (Question, error) {
	// Telegram limits question length; keep identity in the linked watch record.
	units := utf16.Encode([]rune(title))
	if len(units) > 180 {
		title = string(utf16.Decode(units[:180])) + "…"
	}
	q := Question{Topic: PostWatch, Intent: aspectIntent(a), Aspect: a, Scale: aspectScale(a), Version: QuestionV1, Anonymous: true, Revoting: true, Source: "template"}
	var labels []string
	switch a {
	case Overall:
		q.Text = fmt.Sprintf("Как вам «%s» в целом?", title)
		labels = []string{"Совсем не понравился", "Скорее не понравился", "Нейтрально", "Скорее понравился", "Очень понравился"}
	case Pace:
		q.Text = fmt.Sprintf("Как вам темп фильма «%s»?", title)
		labels = []string{"Хотелось быстрее", "В самый раз", "Хотелось спокойнее"}
	case Engagement:
		q.Text = fmt.Sprintf("Насколько «%s» удерживал ваше внимание?", title)
		labels = []string{"Часто отвлекался(ась)", "Местами увлекал", "Увлекал почти всё время"}
	case Characters:
		q.Text = fmt.Sprintf("Насколько вам было интересно следить за героями «%s»?", title)
		labels = []string{"Почти не интересно", "Интересны отдельные герои", "Интересны большинство героев"}
	case Visuals:
		q.Text = fmt.Sprintf("Как вам визуальная сторона «%s»?", title)
		labels = []string{"Скорее не понравилась", "Нейтрально", "Скорее понравилась"}
	default:
		return Question{}, ErrInvalid
	}
	for i, text := range labels {
		q.Options = append(q.Options, Option{Key: fmt.Sprintf("%s:%d", a, i+1), Text: text, Substantive: true})
	}
	q.Options = append(q.Options, Option{Key: "not_watched", Text: "Не смотрел(а)"}, Option{Key: "cannot_rate", Text: "Не могу оценить"})
	return q, q.Validate()
}

func (p ReviewPlan) coverObservations(c *Coverage, observations []Observation) {
	for _, o := range observations {
		if o.Answers() < p.MinimumAnswers {
			continue
		}
		if p.compatibleResearch(o) {
			c.Covered[o.Question.PlanTarget()] = true
			continue
		}
		q := o.Question
		if q.Version != QuestionV1 || q.Topic != PostWatch || q.Scale != aspectScale(q.Aspect) || q.Intent != aspectIntent(q.Aspect) {
			continue
		}
		if q.Aspect == Overall {
			c.Overall = true
		} else {
			c.Covered[q.Aspect] = true
		}
	}
}

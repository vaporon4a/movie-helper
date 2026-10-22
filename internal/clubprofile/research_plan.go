package clubprofile

import "reflect"

const CoverageV2 = "review-coverage-v2"

func ResearchReviewPlan(id string, questions []Question) (ReviewPlan, error) {
	p := DefaultReviewPlan()
	p.Version = CoverageV2
	p.PreparationID = id
	p.Questions = questions
	p.Targets = nil
	for _, q := range questions {
		p.Targets = append(p.Targets, q.PlanTarget())
	}
	return p, p.Validate()
}
func (p ReviewPlan) validateResearchTargets() error {
	if p.PreparationID == "" || len(p.Questions) != 4 {
		return ErrInvalid
	}
	seen := map[Aspect]bool{}
	var first *MeasurementDescriptor
	for i, q := range p.Questions {
		if q.Validate() != nil || q.Version != QuestionV2 || q.Measurement.RegistryVersion != RegistryV2 || p.Targets[i] != q.PlanTarget() || seen[q.PlanTarget()] {
			return ErrInvalid
		}
		d := q.Measurement
		if first != nil && (d.TMDBID != first.TMDBID || d.WatchRevision != first.WatchRevision || d.DossierID != first.DossierID) {
			return ErrInvalid
		}
		first = d
		seen[q.PlanTarget()] = true
	}
	return nil
}
func (p ReviewPlan) QuestionFor(target Aspect, title string) (Question, error) {
	if p.Version == CoverageV2 && target != Overall {
		for _, q := range p.Questions {
			if q.PlanTarget() == target {
				return q, q.Validate()
			}
		}
		return Question{}, ErrInvalid
	}
	return ReviewQuestion(target, title)
}
func (p ReviewPlan) compatibleResearch(o Observation) bool {
	if p.Version != CoverageV2 || o.Question.Version != QuestionV2 || o.Question.Validate() != nil || o.Result.ValidateFinal(o.Question, o.Result.PollID) != nil {
		return false
	}
	for _, q := range p.Questions {
		// Description is transport text added at reservation, not a new wording.
		actual := o.Question
		actual.Description = q.Description
		if reflect.DeepEqual(q, actual) {
			return true
		}
	}
	return false
}

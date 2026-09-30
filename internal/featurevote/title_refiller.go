package featurevote

import (
	"context"
	"time"

	"github.com/vaporon4a/movie-helper/internal/aiwork"
)

type BackgroundTitleGenerator interface {
	TryTitle(context.Context, string) (string, bool)
}

type TitleRefillRepository interface {
	FeatureForTitle(context.Context, int64) (Idea, error)
	SaveFeatureTitle(context.Context, int64, string) error
}

type TitleRefiller struct {
	Store     TitleRefillRepository
	Generator BackgroundTitleGenerator
}

func (r TitleRefiller) Execute(ctx context.Context, work aiwork.Work, _ time.Time) (aiwork.Outcome, error) {
	if work.Kind != aiwork.FeatureTitle {
		return aiwork.Outcome{Done: true}, nil
	}
	idea, err := r.Store.FeatureForTitle(ctx, work.ScopeID)
	if err != nil {
		return aiwork.Outcome{}, err
	}
	if idea.State != IdeaActive || idea.Title != "" {
		return aiwork.Outcome{Done: true}, nil
	}
	if r.Generator == nil {
		return aiwork.Outcome{After: 6 * time.Hour, Reason: "provider_disabled"}, nil
	}
	title, generated := r.Generator.TryTitle(ctx, idea.Text)
	if !generated {
		return aiwork.Outcome{After: 30 * time.Minute, Reason: "title_generation_failed"}, nil
	}
	if err = r.Store.SaveFeatureTitle(ctx, idea.ID, title); err != nil {
		return aiwork.Outcome{}, err
	}
	return aiwork.Outcome{Done: true}, nil
}

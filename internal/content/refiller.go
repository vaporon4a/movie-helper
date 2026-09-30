package content

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/vaporon4a/movie-helper/internal/ai"
	"github.com/vaporon4a/movie-helper/internal/aiwork"
	"github.com/vaporon4a/movie-helper/internal/daily"
)

const backgroundStockTarget = 5

type RefillRepository interface {
	AIStock(context.Context, int64, string, time.Time) (int, error)
	SaveAIItems(context.Context, int64, string, []daily.Item, time.Time) (int, error)
}

type CandidateProvider interface {
	Candidates(context.Context, string, int64) ([]daily.Item, error)
}

type Refiller struct {
	Store    RefillRepository
	Provider CandidateProvider
	Log      *slog.Logger
}

func (r *Refiller) Execute(ctx context.Context, work aiwork.Work, now time.Time) (aiwork.Outcome, error) {
	kind, supported := refillKind(work.Kind)
	if !supported {
		return aiwork.Outcome{Done: true}, nil
	}
	stock, err := r.Store.AIStock(ctx, work.ScopeID, kind, now)
	if err != nil {
		return aiwork.Outcome{}, err
	}
	if stock >= backgroundStockTarget {
		return aiwork.Outcome{Done: true}, nil
	}
	if r.Provider == nil {
		return aiwork.Outcome{After: 6 * time.Hour, Reason: "provider_disabled"}, nil
	}
	fetchCtx, cancel := context.WithTimeout(ctx, daily.FetchTimeout)
	candidates, err := r.Provider.Candidates(fetchCtx, kind, work.ScopeID)
	cancel()
	if err != nil {
		return refillFailure(err, now), nil
	}
	if len(candidates) == 0 {
		return aiwork.Outcome{After: 15 * time.Minute, Reason: "no_approved_candidate"}, nil
	}
	candidates = candidates[:min(len(candidates), backgroundStockTarget-stock)]
	return r.saveCandidates(ctx, work, kind, candidates, now)
}

func refillKind(workKind string) (string, bool) {
	switch workKind {
	case aiwork.FactRefill:
		return daily.Fact, true
	case aiwork.MemeRefill:
		return daily.Meme, true
	default:
		return "", false
	}
}

func refillFailure(err error, now time.Time) aiwork.Outcome {
	reason := preparationCode(err)
	after := time.Duration(0)
	if problem, ok := errors.AsType[*daily.PreviewError](err); ok {
		after = min(problem.After, 5*time.Minute)
	}
	if reason == "local_daily_limit" {
		after = untilNextAIWindow(now)
	}
	return aiwork.Outcome{After: after, Reason: reason}
}

func (r *Refiller) saveCandidates(ctx context.Context, work aiwork.Work, kind string, candidates []daily.Item, now time.Time) (aiwork.Outcome, error) {
	inserted, err := r.Store.SaveAIItems(ctx, work.ScopeID, kind, candidates, now)
	if err != nil {
		return aiwork.Outcome{}, err
	}
	stock, err := r.Store.AIStock(ctx, work.ScopeID, kind, now)
	if err != nil {
		return aiwork.Outcome{}, err
	}
	if r.Log != nil {
		r.Log.Info("AI stock updated", "chat_id", work.ScopeID, "kind", kind, "inserted", inserted, "stock", stock, "target", backgroundStockTarget)
	}
	if stock >= backgroundStockTarget {
		return aiwork.Outcome{Done: true}, nil
	}
	if inserted == 0 {
		return aiwork.Outcome{After: 15 * time.Minute, Reason: "duplicate_candidates"}, nil
	}
	return aiwork.Outcome{After: 15 * time.Minute, Reason: "stock_below_target"}, nil
}

func preparationCode(err error) string {
	if problem, ok := errors.AsType[*daily.PreviewError](err); ok && problem.Code != "" {
		return problem.Code
	}
	if rejected, ok := errors.AsType[*ai.RejectionError](err); ok {
		return "rejected:" + rejected.Stage + ":" + rejected.Reason
	}
	if validation, ok := errors.AsType[*ai.ValidationError](err); ok {
		return "validation_rejected:" + validation.Reason
	}
	return "source_unavailable"
}

func untilNextAIWindow(now time.Time) time.Duration {
	utc := now.UTC()
	next := time.Date(utc.Year(), utc.Month(), utc.Day()+1, 0, 5, 0, 0, time.UTC)
	return next.Sub(now)
}

package content

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/vaporon4a/movie-helper/internal/ai"
	"github.com/vaporon4a/movie-helper/internal/aiwork"
	"github.com/vaporon4a/movie-helper/internal/filmresearch"
)

type ResearchPreparer struct {
	Store              filmresearch.WorkRepository
	Sources            []filmresearch.Source
	Primary, Secondary ai.Generator
	Log                *slog.Logger
}

func (r ResearchPreparer) Execute(ctx context.Context, w aiwork.Work, now time.Time) (aiwork.Outcome, error) {
	step, cancel := context.WithTimeout(ctx, 110*time.Second)
	defer cancel()
	j, err := r.Store.ResearchJob(step, w.ScopeID)
	if errors.Is(err, filmresearch.ErrConflict) {
		return aiwork.Outcome{Done: true}, nil
	}
	if err != nil {
		return aiwork.Outcome{}, err
	}
	if j.Stage != "sources" && j.Stage != "generate" && j.Stage != "audit" {
		return aiwork.Outcome{Done: true}, nil
	}
	if w.Attempts > 16 {
		return r.checkpoint(step, j, "rejected", now)
	}
	if j.Stage == "sources" {
		return r.sources(step, j, now)
	}
	secondary := r.Primary == nil || (r.Secondary != nil && (researchDeferrals(w.LastErrorCode) >= 2 || w.LastErrorCode == researchFallback))
	provider := r.Primary
	name := "gemini"
	if provider == nil || secondary {
		provider = r.Secondary
		name = "groq"
	}
	if provider == nil {
		return aiwork.Outcome{After: 6 * time.Hour, Reason: "research_provider_disabled"}, nil
	}
	next, err := r.generate(step, &j, provider, name)
	if err != nil {
		return r.failure(step, w, j, err, secondary, now)
	}
	return r.checkpoint(step, j, next, now)
}
func (r ResearchPreparer) sources(ctx context.Context, j filmresearch.Job, now time.Time) (aiwork.Outcome, error) {
	var sources []filmresearch.SourceSnapshot
	for _, source := range r.Sources {
		got, err := source.ResearchSources(ctx, j.Film, now)
		if err == nil {
			sources = append(sources, got...)
		} else if r.Log != nil {
			r.Log.Warn("film research source unavailable", "session_id", j.SessionID, "reason", "source_failed")
		}
	}
	if len(sources) == 0 {
		return aiwork.Outcome{After: time.Hour, Reason: "research_no_sources"}, nil
	}
	b, err := filmresearch.NewBundle(j, sources)
	if err != nil {
		return aiwork.Outcome{}, err
	}
	j.Bundle = b
	return r.checkpoint(ctx, j, "generate", now)
}
func (r ResearchPreparer) generate(ctx context.Context, j *filmresearch.Job, g ai.Generator, name string) (string, error) {
	input, err := filmresearch.GenerationInput(j.Bundle, j.Audit)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	instruction := filmresearch.GeneratorPrompt
	auditing := j.Stage == "audit"
	if auditing {
		instruction = filmresearch.AuditPrompt
	}
	result, err := g.Generate(ctx, instruction, []ai.Part{{Text: string(raw), ResponseSchema: filmresearch.ResponseSchema(auditing)}})
	if err != nil {
		return "", err
	}
	if result.Index == nil || *result.Index != 0 {
		return "", filmresearch.ErrInvalid
	}
	if auditing {
		return auditResearch(j, result.Structured, researchProvenance(g, name, filmresearch.PolicyV1+"-audit"))
	}
	var proposal filmresearch.Proposal
	if err = filmresearch.Decode(result.Structured, &proposal); err != nil {
		return "", err
	}
	proposal, err = filmresearch.ResolveReferences(j.Bundle, proposal)
	if err != nil {
		return "", err
	}
	b, err := j.Bundle.WithProposal(proposal, researchProvenance(g, name, filmresearch.PolicyV1))
	if err != nil {
		return "", err
	}
	j.Bundle = b
	return "audit", nil
}
func (r ResearchPreparer) checkpoint(ctx context.Context, j filmresearch.Job, next string, now time.Time) (aiwork.Outcome, error) {
	err := r.Store.CheckpointResearch(ctx, j, next, now)
	if errors.Is(err, filmresearch.ErrConflict) {
		return aiwork.Outcome{Done: true}, nil
	}
	if err != nil {
		return aiwork.Outcome{}, err
	}
	if r.Log != nil {
		r.Log.Info("film research checkpoint", "session_id", j.SessionID, "from", j.Stage, "to", next, "repairs", j.Repairs, "policy", filmresearch.PolicyV1)
	}
	return aiwork.Outcome{Done: next == "ready" || next == "rejected", After: time.Minute, Reason: "research_" + next}, nil
}

func (r ResearchPreparer) failure(ctx context.Context, w aiwork.Work, j filmresearch.Job, err error, secondary bool, now time.Time) (aiwork.Outcome, error) {
	if r.Log != nil {
		r.Log.Warn("film research stage failed", "session_id", j.SessionID, "stage", j.Stage, "reason", researchFailureReason(err))
	}
	if errors.Is(err, filmresearch.ErrInvalid) {
		if j.Repairs == 0 {
			j.Repairs = 1
			return r.checkpoint(ctx, j, "generate", now)
		}
		return r.checkpoint(ctx, j, "rejected", now)
	}
	if errors.Is(err, ai.ErrDailyLimit) && !secondary && r.Primary != nil && r.Secondary != nil {
		return aiwork.Outcome{After: time.Minute, Reason: researchFallback}, nil
	}
	return researchRetry(err, now, !secondary, researchDeferrals(w.LastErrorCode)), nil
}

func researchProvenance(g ai.Generator, name, prompt string) filmresearch.Provenance {
	model := "unknown"
	if m, ok := g.(interface{ ModelName() string }); ok {
		model = m.ModelName()
	}
	p := filmresearch.Provenance{Provider: name, Model: model, PromptVersion: prompt, ValidatorVersion: filmresearch.PolicyV1}
	if settings, ok := g.(interface {
		GenerationSettings() (string, *float64, int)
	}); ok {
		p.ReasoningEffort, p.Temperature, p.MaxOutputTokens = settings.GenerationSettings()
	}
	return p
}

func auditResearch(j *filmresearch.Job, raw []byte, provenance filmresearch.Provenance) (string, error) {
	var audit filmresearch.Audit
	if err := filmresearch.Decode(raw, &audit); err != nil {
		return "", err
	}
	audit.Provenance = provenance
	j.Audit = audit
	if !audit.GroundedAccepted(j.Bundle) {
		return "", filmresearch.ErrInvalid
	}
	return "ready", nil
}

func researchFailureReason(err error) string {
	if errors.Is(err, filmresearch.ErrInvalid) {
		return "research_contract_rejected"
	}
	return failureReason(err)
}

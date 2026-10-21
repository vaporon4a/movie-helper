package telegram

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/vaporon4a/movie-helper/internal/clubprofile"
)

type ProfileSender struct {
	api PollAPI
	log *slog.Logger
	now func() time.Time
}

func NewProfileSender(api PollAPI, log *slog.Logger, now func() time.Time) (ProfileSender, error) {
	if api == nil || log == nil || now == nil {
		return ProfileSender{}, errMissingDependency
	}
	return ProfileSender{api: api, log: log, now: now}, nil
}

func (s ProfileSender) OpenProfilePoll(ctx context.Context, p clubprofile.Poll) (string, int, error) {
	if err := p.Question.Validate(); err != nil {
		return "", 0, err
	}
	options := make([]models.InputPollOption, len(p.Question.Options))
	for i, o := range p.Question.Options {
		options[i] = models.InputPollOption{Text: o.Text}
	}
	anonymous := true
	m, err := s.api.SendPoll(ctx, &bot.SendPollParams{ChatID: p.ChatID, Question: p.Question.Text, Description: p.Question.Description, Options: options, IsAnonymous: &anonymous, AllowsRevoting: true, HideResultsUntilCloses: true, Type: "regular"})
	if err != nil {
		return "", 0, s.failure(ctx, p, "open", err)
	}
	if m == nil || m.Poll == nil || m.Poll.ID == "" || m.ID <= 0 {
		return "", 0, &clubprofile.DeliveryError{Kind: clubprofile.DeliveryUnknown, Reason: "empty_receipt"}
	}
	return m.Poll.ID, m.ID, nil
}

type DeleteAPI interface {
	DeleteMessage(context.Context, *bot.DeleteMessageParams) (bool, error)
}

type ProfileCleaner struct{ API DeleteAPI }

func (s ProfileCleaner) DeleteProfilePoll(ctx context.Context, j clubprofile.CleanupJob) error {
	ok, err := s.API.DeleteMessage(ctx, &bot.DeleteMessageParams{ChatID: j.ChatID, MessageID: j.MessageID})
	if err == nil {
		if !ok {
			return &clubprofile.DeliveryError{Kind: clubprofile.DeliveryUnknown, Reason: "empty_receipt"}
		}
		return nil
	}
	if errors.Is(err, bot.ErrorBadRequest) && strings.Contains(strings.ToLower(err.Error()), "message to delete not found") {
		return nil
	}
	return classifyProfileDelivery(err)
}

func (s ProfileSender) CloseProfilePoll(ctx context.Context, p clubprofile.Poll) (clubprofile.PollResult, error) {
	poll, err := s.api.StopPoll(ctx, &bot.StopPollParams{ChatID: p.ChatID, MessageID: p.MessageID})
	if err != nil {
		return clubprofile.PollResult{}, s.failure(ctx, p, "close", err)
	}
	if poll == nil {
		return clubprofile.PollResult{}, clubprofile.ErrInvalid
	}
	return profileResult(poll, clubprofile.SourceStop, 0, s.now()), nil
}

func profileResult(p *models.Poll, source clubprofile.ResultSource, updateID int64, now time.Time) clubprofile.PollResult {
	counts := make([]int, len(p.Options))
	for i, o := range p.Options {
		counts[i] = o.VoterCount
	}
	total := p.TotalVoterCount
	return clubprofile.PollResult{PollID: p.ID, Counts: counts, Total: &total, Closed: p.IsClosed, Source: source, ReceivedAt: now, UpdateID: updateID}
}

func (s ProfileSender) failure(ctx context.Context, p clubprofile.Poll, operation string, err error) error {
	typed := classifyProfileDelivery(err)
	s.log.WarnContext(ctx, "telegram profile poll failed", "poll_id", p.ID, "chat_id", p.ChatID, "operation", operation, "reason", typed.Reason)
	return typed
}

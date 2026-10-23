// Package recovery exposes only explicit, chat-scoped resolution of known operations.
package recovery

import (
	"context"
	"errors"
	"time"
)

var ErrConflict = errors.New("recovery state changed")

type Issue struct {
	Domain                      string
	ID                          int64
	State, Reason, Label, Stamp string
	Actions                     []Action
}
type Action struct{ Key, Label, Consequence string }
type Confirmation struct {
	Token   string
	ChatID  int64
	PanelID int
	Issue   Issue
	Action  string
	Expires time.Time
}
type Repository interface {
	RecoveryIssues(context.Context, int64, time.Time) ([]Issue, error)
	PrepareRecovery(context.Context, Confirmation, time.Time) (Confirmation, error)
	ApplyRecovery(context.Context, string, int64, int, time.Time) error
}
type Service struct{ store Repository }

func New(store Repository) *Service { return &Service{store} }
func (s *Service) Issues(ctx context.Context, chat int64, now time.Time) ([]Issue, error) {
	return s.store.RecoveryIssues(ctx, chat, now)
}
func (s *Service) Prepare(ctx context.Context, c Confirmation, now time.Time) (Confirmation, error) {
	return s.store.PrepareRecovery(ctx, c, now)
}
func (s *Service) Apply(ctx context.Context, token string, chat int64, panel int, now time.Time) error {
	return s.store.ApplyRecovery(ctx, token, chat, panel, now)
}

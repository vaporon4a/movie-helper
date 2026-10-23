package interaction

import (
	"context"
	"time"
)

type Service struct {
	inputs IntentRepository
	panels PanelRepository
}

func New(store Repository) *Service { return &Service{inputs: store, panels: store} }
func (s *Service) NewIntent(ctx context.Context, i Intent, now time.Time) (Intent, error) {
	var err error
	i.Token, err = Token()
	if err != nil {
		return i, err
	}
	i.Expires = now.Add(10 * time.Minute)
	i.State = "pending"
	return i, s.inputs.CreateDailyIntent(ctx, i, now)
}
func (s *Service) CancelActorInputs(ctx context.Context, chat, actor int64) ([]int, error) {
	return s.inputs.CancelDailyActorInputs(ctx, chat, actor)
}
func (s *Service) Input(ctx context.Context, token string, chat int64, panel int, now time.Time) (Intent, error) {
	return s.inputs.DailyIntent(ctx, token, chat, panel, now)
}
func (s *Service) BindPrompt(ctx context.Context, token string, prompt int) error {
	return s.inputs.BindDailyPrompt(ctx, token, prompt)
}
func (s *Service) Reply(ctx context.Context, chat, actor int64, prompt int, now time.Time) (Intent, error) {
	return s.inputs.DailyReplyIntent(ctx, chat, actor, prompt, now)
}
func (s *Service) CancelPanel(ctx context.Context, chat int64, panel int, now time.Time) ([]int, error) {
	return s.inputs.CancelDailyIntents(ctx, chat, panel, now)
}
func (s *Service) ClosePanel(ctx context.Context, chat int64, panel int) error {
	return s.panels.CloseDailyPanel(ctx, chat, panel)
}
func (s *Service) Closed(ctx context.Context, chat int64, panel int) (bool, error) {
	return s.panels.DailyPanelClosed(ctx, chat, panel)
}
func (s *Service) PanelScreen(ctx context.Context, chat int64, panel int) (string, error) {
	return s.panels.PanelScreen(ctx, chat, panel)
}
func (s *Service) SetPanelScreen(ctx context.Context, chat int64, panel int, screen string) error {
	return s.panels.SetPanelScreen(ctx, chat, panel, screen)
}

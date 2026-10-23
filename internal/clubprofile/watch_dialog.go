package clubprofile

import (
	"context"
	"time"
)

type WatchConfirmationRepository interface {
	AttachWatchDialog(context.Context, int64, int64, int, string, time.Time) error
	ConfirmWatchDialog(context.Context, int64, int64, int, string, time.Time) (WatchDraft, error)
	DateWatchDialog(context.Context, int64, int64, int, string, time.Time) (WatchDraft, error)
}

func (s *WatchService) BeginDialog(ctx context.Context, op, chat int64, panel int, text, token string) (WatchDraft, error) {
	if !s.allowed[chat] {
		return WatchDraft{}, ErrInvalid
	}
	zone, err := s.drafts.ProfileZone(ctx, chat)
	if err != nil {
		return WatchDraft{}, err
	}
	in, err := ParseWatchInput(text, zone, s.now())
	if err != nil {
		return WatchDraft{}, err
	}
	in.Provisional = true
	d, err := s.drafts.CreateWatchDraft(ctx, op, chat, in, s.now())
	if err != nil {
		return d, err
	}
	if err = s.confirmations.AttachWatchDialog(ctx, chat, d.ID, panel, token, s.now()); err != nil {
		return d, err
	}
	d, err = s.drafts.WatchDraft(ctx, chat, d.ID)
	if err != nil {
		return d, err
	}
	return s.resolve(ctx, d)
}
func (s *WatchService) ConfirmDialog(ctx context.Context, chat, id int64, version int, date string) (WatchDraft, error) {
	if !s.allowed[chat] {
		return WatchDraft{}, ErrInvalid
	}
	d, err := s.confirmations.ConfirmWatchDialog(ctx, chat, id, version, date, s.now())
	if err == nil {
		s.log.InfoContext(ctx, "viewing confirmed", "chat_id", chat, "draft_id", id, "watch_id", d.WatchID)
	}
	return d, err
}

func (s *WatchService) Dialog(ctx context.Context, chat, id int64, panel int) (WatchDraft, error) {
	if !s.allowed[chat] {
		return WatchDraft{}, ErrInvalid
	}
	d, err := s.drafts.WatchDraft(ctx, chat, id)
	if err != nil {
		return d, err
	}
	if d.MessageID != panel || d.Closed || !d.Input.Provisional || !s.now().Before(d.Expires) {
		return d, ErrConflict
	}
	return d, nil
}
func (s *WatchService) DateDialog(ctx context.Context, chat, id int64, version int, date string) (WatchDraft, error) {
	if !s.allowed[chat] {
		return WatchDraft{}, ErrInvalid
	}
	return s.confirmations.DateWatchDialog(ctx, chat, id, version, date, s.now())
}

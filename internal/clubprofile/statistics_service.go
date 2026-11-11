package clubprofile

import (
	"context"
	"log/slog"
	"time"
)

type StatisticsReader interface {
	StatisticsInput(context.Context, int64) (StatisticsInput, error)
}
type StatisticsSettings struct {
	ChatID            int64
	Zone, Clock       string
	Weekday, Revision int
	Enabled           bool
	Effective         time.Time
}
type StatisticsSettingsRepository interface {
	StatisticsSettings(context.Context, int64) (StatisticsSettings, error)
	SetStatisticsSettings(context.Context, StatisticsSettings, time.Time) error
}
type StatisticsService struct {
	Reports  StatisticsHistoryRepository
	Reader   StatisticsReader
	Settings StatisticsSettingsRepository
	Allowed  map[int64]bool
	Now      func() time.Time
	Log      *slog.Logger
}

func (s *StatisticsService) Snapshot(ctx context.Context, chat int64, mode string) (StatisticsSnapshot, error) {
	if !s.Allowed[chat] {
		return StatisticsSnapshot{}, ErrNotFound
	}
	in, err := s.Reader.StatisticsInput(ctx, chat)
	if err != nil {
		return StatisticsSnapshot{}, err
	}
	if in.Zone == "" {
		in.Zone = "UTC"
	}
	loc, err := time.LoadLocation(in.Zone)
	if err != nil || in.Zone == "" {
		return StatisticsSnapshot{}, ErrInvalid
	}
	now := s.Now()
	end := now.In(loc)
	var period StatisticsPeriod
	switch mode {
	case "week":
		period = StatisticsPeriod{end.AddDate(0, 0, -7), end}
	case "month":
		period = StatisticsPeriod{end.AddDate(0, -1, 0), end}
	}
	out := BuildStatistics(in, period, now)
	s.Log.Debug("club statistics projected", "chat_id", chat, "polls", out.Polls, "claims", len(out.Claims), "invalid", out.Invalid, "version", out.Version)
	return out, nil
}
func (s *StatisticsService) Configuration(ctx context.Context, chat int64) (StatisticsSettings, error) {
	if !s.Allowed[chat] {
		return StatisticsSettings{}, ErrNotFound
	}
	return s.Settings.StatisticsSettings(ctx, chat)
}
func (s *StatisticsService) Change(ctx context.Context, chat int64, revision int, action string) error {
	sc, err := s.Configuration(ctx, chat)
	if err != nil {
		return err
	}
	if revision != sc.Revision {
		return ErrConflict
	}
	switch action {
	case "toggle":
		sc.Enabled = !sc.Enabled
	case "day":
		sc.Weekday = (sc.Weekday + 1) % 7
	case "hour", "minute":
		clock, e := time.Parse("15:04", sc.Clock)
		if e != nil {
			return e
		}
		step := time.Hour
		if action == "minute" {
			step = 15 * time.Minute
		}
		sc.Clock = clock.Add(step).Format("15:04")
	default:
		return ErrInvalid
	}
	if err = s.Settings.SetStatisticsSettings(ctx, sc, s.Now()); err == nil {
		s.Log.Info("club statistics settings changed", "chat_id", chat, "enabled", sc.Enabled, "weekday", sc.Weekday, "clock", sc.Clock)
	}
	return err
}

// DigestPeriod covers seven complete calendar days, not seven 24-hour spans.
func DigestPeriod(slot time.Time) StatisticsPeriod {
	end := time.Date(slot.Year(), slot.Month(), slot.Day(), 0, 0, 0, 0, slot.Location())
	return StatisticsPeriod{end.AddDate(0, 0, -7), end}
}
func DigestWeekKey(slot time.Time) string {
	offset := (int(slot.Weekday()) + 6) % 7
	return slot.AddDate(0, 0, -offset).Format("2006-01-02")
}
func DigestSlot(sc StatisticsSettings, now time.Time) (time.Time, error) {
	at, err := WeeklySlot(now, sc.Weekday, sc.Clock, sc.Zone)
	if err != nil {
		return at, err
	}
	if at.After(now) {
		return WeeklySlot(now.AddDate(0, 0, -7), sc.Weekday, sc.Clock, sc.Zone)
	}
	return at, nil
}
func (s *StatisticsService) Preview(ctx context.Context, chat int64) (StatisticsSnapshot, error) {
	sc, err := s.Configuration(ctx, chat)
	if err != nil {
		return StatisticsSnapshot{}, err
	}
	at, err := WeeklySlot(s.Now(), sc.Weekday, sc.Clock, sc.Zone)
	if err != nil {
		return StatisticsSnapshot{}, err
	}
	if at.Before(s.Now()) {
		at, err = WeeklySlot(s.Now().AddDate(0, 0, 7), sc.Weekday, sc.Clock, sc.Zone)
	}
	if err != nil {
		return StatisticsSnapshot{}, err
	}
	in, err := s.Reader.StatisticsInput(ctx, chat)
	if err != nil {
		return StatisticsSnapshot{}, err
	}
	out := BuildStatistics(in, DigestPeriod(at), s.Now())
	out.Claims = SelectStatisticsClaims(out.Claims, 3)
	return out, nil
}

type StatisticsReport struct {
	ID                  int64
	State, Reason, Text string
	Snapshot            StatisticsSnapshot
}
type StatisticsHistoryRepository interface {
	StatisticsReports(context.Context, int64) ([]StatisticsReport, error)
}

func (s *StatisticsService) History(ctx context.Context, chat int64) ([]StatisticsReport, error) {
	if !s.Allowed[chat] {
		return nil, ErrNotFound
	}
	if s.Reports == nil {
		return nil, nil
	}
	return s.Reports.StatisticsReports(ctx, chat)
}

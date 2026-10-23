// Package agenda projects upcoming club activity without reserving or sending it.
package agenda

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/vaporon4a/movie-helper/internal/clubprofile"
	"github.com/vaporon4a/movie-helper/internal/clubreminder"
	"github.com/vaporon4a/movie-helper/internal/daily"
	"github.com/vaporon4a/movie-helper/internal/featurevote"
)

type Event struct {
	At                                    time.Time
	Zone, Kind, Label, Status, Provenance string
	ID                                    int64
}
type View struct {
	Zone        string
	Events      []Event
	Queue       []string
	Meeting     string
	Unavailable []string
}
type DailySource interface {
	Schedules(context.Context, int64) ([]daily.Schedule, error)
}
type MovieSource interface {
	ReadMoviesConfig(context.Context, int64) (clubprofile.ScheduleConfig, error)
	ProfileSchedules(context.Context, int64) ([]clubprofile.Schedule, error)
	ProfilePolls(context.Context, int64, clubprofile.PollState) ([]clubprofile.Poll, error)
	ProfileWatches(context.Context, int64) ([]clubprofile.WatchEvent, error)
	ProfileSessions(context.Context, int64) ([]clubprofile.ReviewSession, error)
}
type ClubSource interface {
	ReadClubConfig(context.Context, int64) (clubreminder.ScheduleConfig, error)
	ClubSettings(context.Context, int64, time.Time) (clubreminder.Settings, error)
}
type IdeaSource interface {
	FeatureSettings(context.Context, int64) (featurevote.SettingsView, error)
}
type Source interface {
	DailySource
	MovieSource
	ClubSource
	IdeaSource
}
type Service struct{ source Source }

func New(source Source) *Service { return &Service{source} }
func (s *Service) Read(ctx context.Context, chat int64, now time.Time) View {
	v := View{}
	if c, err := s.source.ReadMoviesConfig(ctx, chat); err == nil {
		v.Zone = c.Zone
	} else {
		v.Unavailable = append(v.Unavailable, "часовой пояс")
	}
	s.daily(ctx, chat, now, &v)
	s.movies(ctx, chat, now, &v)
	s.club(ctx, chat, now, &v)
	s.ideas(ctx, chat, now, &v)
	v.Events = slices.DeleteFunc(v.Events, func(e Event) bool {
		loc, err := time.LoadLocation(e.Zone)
		if err != nil {
			loc, err = time.LoadLocation(v.Zone)
		}
		if err != nil {
			loc = time.UTC
		}
		return e.At.After(now.In(loc).AddDate(0, 0, 7))
	})
	slices.SortFunc(v.Events, func(a, b Event) int {
		if c := a.At.Compare(b.At); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Kind, b.Kind); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})
	return v
}
func (s *Service) daily(ctx context.Context, chat int64, now time.Time, v *View) {
	schedules, err := s.source.Schedules(ctx, chat)
	if err != nil {
		v.Unavailable = append(v.Unavailable, "мемы и факты")
		return
	}
	for _, sc := range schedules {
		for _, at := range daily.UpcomingSlots(sc, now, 7) {
			label := "Мем дня"
			if sc.Kind == daily.Fact {
				label = "Факт о кино"
			}
			v.Events = append(v.Events, Event{At: at, Zone: sc.Zone, Kind: sc.Kind, Label: label, Status: "расчётное время", Provenance: "daily_schedule"})
		}
	}
}
func (s *Service) movies(ctx context.Context, chat int64, now time.Time, v *View) {
	schedules, err := s.source.ProfileSchedules(ctx, chat)
	if err != nil {
		v.Unavailable = append(v.Unavailable, "киноопросы")
		return
	}
	polls, err := s.source.ProfilePolls(ctx, chat, "")
	if err != nil {
		v.Unavailable = append(v.Unavailable, "состояние опросов")
		return
	}
	taken := appendActualPolls(v, polls, now)
	appendMovieWindows(v, schedules, taken, now)
	s.movieQueue(ctx, chat, v)
}
func (s *Service) movieQueue(ctx context.Context, chat int64, v *View) {
	watches, e := s.source.ProfileWatches(ctx, chat)
	if e != nil {
		v.Unavailable = append(v.Unavailable, "просмотры")
		return
	}
	sessions, e := s.source.ProfileSessions(ctx, chat)
	if e != nil {
		v.Unavailable = append(v.Unavailable, "очередь обсуждений")
		return
	}
	for _, session := range clubprofile.ReviewQueue(sessions) {
		for _, w := range watches {
			if w.ID == session.WatchID {
				v.Queue = append(v.Queue, w.Film.Label()+" · "+session.State)
			}
		}
	}
}
func (s *Service) club(ctx context.Context, chat int64, now time.Time, v *View) {
	c, err := s.source.ClubSettings(ctx, chat, now)
	if err != nil {
		v.Unavailable = append(v.Unavailable, "встречи и напоминания")
		return
	}
	if c.Revision == 0 {
		return
	}
	if at, e := clubreminder.NextMeeting(c, now); e == nil {
		v.Meeting = at.Format("02.01.2006") + " (" + c.Zone + ", время начала не задано)"
	}
	config, e := s.source.ReadClubConfig(ctx, chat)
	if e != nil {
		v.Unavailable = append(v.Unavailable, "состояние напоминаний")
		return
	}
	if !c.Enabled || !config.Active {
		return
	}
	slots, err := clubreminder.Slots(c, now)
	if err != nil {
		v.Unavailable = append(v.Unavailable, "напоминания")
		return
	}
	loc, e := time.LoadLocation(c.Zone)
	if e != nil {
		return
	}
	until := now.In(loc).AddDate(0, 0, 7)
	for _, slot := range slots {
		if slot.At.After(now) && !slot.At.After(until) && slot.At.Unix() > c.Effective {
			v.Events = append(v.Events, Event{At: slot.At, Zone: c.Zone, Kind: "club", Label: "Напоминание выбрать фильм", Status: "расчётное время", Provenance: "club_schedule"})
		}
	}
}
func (s *Service) ideas(ctx context.Context, chat int64, now time.Time, v *View) {
	settings, err := s.source.FeatureSettings(ctx, chat)
	if err != nil {
		v.Unavailable = append(v.Unavailable, "голосования за идеи")
		return
	}
	taken := map[int64]bool{}
	for _, r := range settings.Latest {
		if r.State != featurevote.RoundOpen && r.State != featurevote.RoundPlanned {
			continue
		}
		at := time.Unix(r.SlotAt, 0)
		label := "Начало голосования за идеи"
		if r.State == featurevote.RoundOpen {
			at = time.Unix(r.ClosesAt, 0)
			label = "Завершение голосования за идеи"
		}
		if at.Before(now) {
			at = now
		}
		v.Events = append(v.Events, Event{At: at, Zone: settings.Schedule.Zone, Kind: "ideas", Label: label, Status: string(r.State), ID: r.ID, Provenance: "persisted_round"})
		taken[r.SlotAt] = true
	}
	for _, at := range featurevote.UpcomingSlots(settings.Schedule, now, 7) {
		if !taken[at.Unix()] {
			v.Events = append(v.Events, Event{At: at, Zone: settings.Schedule.Zone, Kind: "ideas", Label: "Голосование за идеи", Status: "расчётное время", Provenance: "idea_schedule"})
		}
	}
}

func appendActualPolls(v *View, polls []clubprofile.Poll, now time.Time) map[int64]bool {
	taken := map[int64]bool{}
	for _, p := range polls {
		if p.State != clubprofile.Planned && p.State != clubprofile.Open && p.State != clubprofile.Closing && p.State != clubprofile.Creating {
			continue
		}
		at := p.SlotAt
		label := "Подготовленный киноопрос"
		if p.State == clubprofile.Open || p.State == clubprofile.Closing {
			at = p.ClosesAt
			label = "Завершение открытого киноопроса"
		}
		if at.Before(now) {
			at = now
		}
		v.Events = append(v.Events, Event{At: at, Zone: v.Zone, Kind: "movies", Label: fmt.Sprintf("%s #%d", label, p.ID), Status: string(p.State), Provenance: "persisted_poll", ID: p.ID})
		taken[p.SlotAt.Unix()] = true
	}
	return taken
}

func appendMovieWindows(v *View, schedules []clubprofile.Schedule, taken map[int64]bool, now time.Time) {
	for _, sc := range schedules {
		for _, at := range clubprofile.UpcomingSlots(sc, now, 7) {
			if taken[at.Unix()] {
				continue
			}
			label := "Опрос интересов · " + string(sc.Topic)
			if clubprofile.ResearchSlot(schedules, sc, at) {
				label = "Окно вопросов о просмотренном (или обычный опрос)"
			}
			v.Events = append(v.Events, Event{At: at, Zone: sc.Zone, Kind: "movies", Label: label, Status: "расчётное окно", Provenance: "profile_schedule", ID: sc.ID})
		}
	}
}

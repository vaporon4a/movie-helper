// Package scheduleui coordinates only the three supported schedule editors.
package scheduleui

import (
	"context"
	"errors"
	"time"

	"github.com/vaporon4a/movie-helper/internal/clubprofile"
	"github.com/vaporon4a/movie-helper/internal/clubreminder"
	"github.com/vaporon4a/movie-helper/internal/featurevote"
	"github.com/vaporon4a/movie-helper/internal/interaction"
)

var ErrConflict = errors.New("schedule dialog expired or changed")

type Day struct {
	Clock   string
	Enabled bool
}
type Movies struct {
	Before clubprofile.ScheduleConfig
	Topic  clubprofile.Topic
	Days   [7]Day
}
type Club struct {
	Before clubreminder.ScheduleConfig
	Value  clubreminder.Settings
}
type Ideas struct {
	Before featurevote.ScheduleConfig
	Value  featurevote.Schedule
}
type Draft struct {
	Token, Kind, Step string
	ChatID            int64
	PanelID, Day      int
	Expires           time.Time
	Movies            *Movies
	Club              *Club
	Ideas             *Ideas
}

type Repository interface {
	PutScheduleDialog(context.Context, Draft, string, time.Time) error
	ScheduleDialog(context.Context, string, int64, int, time.Time) (Draft, error)
	CommitScheduleDialog(context.Context, Draft, time.Time) (bool, error)
}

// Domain-specific repositories are separate from the interaction persistence.
type Sources interface {
	clubprofile.ConfigRepository
	clubreminder.ConfigRepository
	featurevote.ConfigRepository
}
type Service struct {
	store   Repository
	sources Sources
}

func New(store Repository, sources Sources) *Service { return &Service{store, sources} }
func (s *Service) Begin(ctx context.Context, chat int64, panel int, kind string, now time.Time) (Draft, error) {
	d := Draft{ChatID: chat, PanelID: panel, Kind: kind, Step: "edit", Expires: now.Add(10 * time.Minute)}
	var err error
	switch kind {
	case "genre", "reference":
		c, e := s.sources.ReadMoviesConfig(ctx, chat)
		if e != nil {
			return d, e
		}
		m := &Movies{Before: c, Topic: clubprofile.Topic(kind)}
		for day := range m.Days {
			m.Days[day].Clock = "09:00"
		}
		for _, sc := range c.Schedules {
			if sc.Topic == m.Topic {
				m.Days[sc.Weekday] = Day{sc.Clock, sc.Enabled}
			}
		}
		d.Movies = m
	case "club":
		c, e := s.sources.ReadClubConfig(ctx, chat)
		if e != nil {
			return d, e
		}
		d.Club = &Club{Before: c, Value: c.Settings}
	case "ideas":
		c, e := s.sources.ReadIdeasConfig(ctx, chat)
		if e != nil {
			return d, e
		}
		d.Ideas = &Ideas{Before: c, Value: c.Schedule}
	default:
		return d, ErrConflict
	}
	d.Token, err = interaction.Token()
	if err != nil {
		return d, err
	}
	return d, s.store.PutScheduleDialog(ctx, d, "", now)
}
func (s *Service) Read(ctx context.Context, token string, chat int64, panel int, now time.Time) (Draft, error) {
	return s.store.ScheduleDialog(ctx, token, chat, panel, now)
}
func (s *Service) Update(ctx context.Context, d Draft, now time.Time) (Draft, error) {
	previous := d.Token
	d.Expires = now.Add(10 * time.Minute)
	var err error
	d.Token, err = interaction.Token()
	if err != nil {
		return d, err
	}
	return d, s.store.PutScheduleDialog(ctx, d, previous, now)
}
func (s *Service) Commit(ctx context.Context, d Draft, now time.Time) (bool, error) {
	return s.store.CommitScheduleDialog(ctx, d, now)
}

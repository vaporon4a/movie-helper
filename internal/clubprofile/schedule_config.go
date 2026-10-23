package clubprofile

import (
	"context"
	"errors"
	"time"
)

var ErrConfigConflict = errors.New("settings changed or invalid")

type ScheduleConfig struct {
	Revision  int64
	Active    bool
	Zone      string
	Schedules []Schedule
}
type SchedulePatch struct {
	Weekday int
	Clock   *string
	Enabled *bool
}
type ConfigChange struct {
	Topic Topic
	Days  []SchedulePatch
}

type ConfigRepository interface {
	ReadMoviesConfig(context.Context, int64) (ScheduleConfig, error)
	PatchMoviesConfig(context.Context, int64, int64, ConfigChange, time.Time) (bool, error)
}

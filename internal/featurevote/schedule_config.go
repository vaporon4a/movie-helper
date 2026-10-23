package featurevote

import (
	"context"
	"errors"
	"time"
)

var ErrConfigConflict = errors.New("settings changed or invalid")

type ScheduleConfig struct {
	Revision int64
	Active   bool
	Zone     string
	Schedule Schedule
}
type ConfigChange struct {
	Weekday *int
	Clock   *string
	Enabled *bool
}

type ConfigRepository interface {
	ReadIdeasConfig(context.Context, int64) (ScheduleConfig, error)
	PatchIdeasConfig(context.Context, int64, int64, ConfigChange, time.Time) (bool, error)
}

package clubreminder

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
	Settings Settings
}
type ConfigChange struct {
	Weekday, Ordinal *int
	Clock            *string
	Enabled          *bool
}

type ConfigRepository interface {
	ReadClubConfig(context.Context, int64) (ScheduleConfig, error)
	PatchClubConfig(context.Context, int64, int64, ConfigChange, time.Time) (bool, error)
}

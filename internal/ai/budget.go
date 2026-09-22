package ai

import (
	"context"
	"errors"
)

type Budget interface {
	AllowAPI(context.Context, string, int) (bool, error)
}

var ErrDailyLimit = errors.New("AI daily request limit reached")

package ai

import (
	"context"
	"errors"
)

type Budget interface {
	AllowAPI(context.Context, string, int) (bool, error)
}

type BudgetReader interface {
	RemainingAPI(context.Context, string, int) (int, error)
}

var ErrDailyLimit = errors.New("AI daily request limit reached")

package interaction

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"
)

type Panel struct {
	ChatID, Requester     int64
	Purpose, Token, State string
	MessageID             int
}
type PanelRegistry interface {
	AcquirePanel(context.Context, int64, int64, string, time.Time) (Panel, error)
	FinishPanel(context.Context, Panel, int, string, time.Time) error
	RetirePanel(context.Context, Panel) error
}

func Token() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

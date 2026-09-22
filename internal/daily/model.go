// Package daily contains the domain shared by storage, Telegram and the scheduler.
package daily

import (
	"errors"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

const Meme = "meme"
const Fact = "fact"

var ErrDuplicate = errors.New("operation already processed")
var ErrConflict = errors.New("item unavailable or state changed")

type Item struct {
	ID, ChatID, AuthorID                  int64
	Kind, Text, Source, Image, Key, State string
}

type Schedule struct {
	ChatID            int64
	Kind, Clock, Zone string
	Enabled           bool
	Moderation        bool
	Effective         int64
}

type Delivery struct {
	ID, ChatID, Deadline, NextAttempt int64
	Kind, Date, State                 string
	Item                              Item
}

// SendError deliberately carries no Telegram response body or token-bearing URL.
type SendError struct {
	Kind  string // retry (explicit rejection), forbidden, permanent, unknown
	After time.Duration
}

func (e *SendError) Error() string { return "delivery: " + e.Kind }

func ValidKind(s string) bool { return s == Meme || s == Fact }

func ValidSource(s string) bool {
	u, err := url.Parse(s)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && len(s) <= 600
}

func (i Item) Validate() error {
	if !ValidKind(i.Kind) || i.ChatID >= 0 || i.Key == "" {
		return errors.New("invalid item")
	}
	if i.Kind == Fact && (strings.TrimSpace(i.Text) == "" || utf8.RuneCountInString(i.Text) > 1500 || !ValidSource(i.Source)) {
		return errors.New("fact needs text (up to 1500 characters) and an HTTPS source")
	}
	if i.Kind == Meme && (i.Image == "" || utf8.RuneCountInString(i.Text) > 200 || (i.Source != "" && !ValidSource(i.Source))) {
		return errors.New("meme needs an image and a short caption")
	}
	return nil
}

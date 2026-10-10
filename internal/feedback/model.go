// Package feedback records publication context and reactions without interpreting taste.
package feedback

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/vaporon4a/movie-helper/internal/daily"
)

const (
	Scheduled = "scheduled"
	Preview   = "preview"
	Delta     = "delta"
	Snapshot  = "snapshot"
	Emoji     = "emoji"
	Custom    = "custom_emoji"
	Paid      = "paid"
)

var ErrInvalid = errors.New("invalid reaction feedback")

// Post is an immutable snapshot; previews do not consume or modify their item.
type Post struct {
	ChatID      int64
	MessageID   int
	Publication string
	PublishedAt time.Time
	Item        daily.Item
}

func (p Post) Validate() error {
	if p.ChatID >= 0 || p.MessageID <= 0 || p.PublishedAt.Unix() <= 0 || !daily.ValidKind(p.Item.Kind) {
		return ErrInvalid
	}
	if p.Publication != Scheduled && p.Publication != Preview {
		return ErrInvalid
	}
	return nil
}

type Reaction struct {
	Type  string `json:"type"`
	Value string `json:"value"`
	Count int64  `json:"count"`
}

// Event contains only reaction values, never the actor's identity or raw update.
// Delta lists are the old/new set of ONE actor; snapshot is the full message count.
type Event struct {
	UpdateID   int64
	ChatID     int64
	MessageID  int
	Mode       string
	OccurredAt int64
	ReceivedAt time.Time
	Before     []Reaction
	After      []Reaction
}

func (e Event) Validate() error {
	if e.UpdateID < 0 || e.ChatID >= 0 || e.MessageID <= 0 || e.OccurredAt <= 0 || e.ReceivedAt.Unix() <= 0 {
		return ErrInvalid
	}
	if e.Mode != Delta && e.Mode != Snapshot {
		return ErrInvalid
	}
	if e.Mode == Snapshot && len(e.Before) != 0 {
		return ErrInvalid
	}
	if !validReactions(e.Before, e.Mode) || !validReactions(e.After, e.Mode) {
		return ErrInvalid
	}
	return nil
}

func validReactions(reactions []Reaction, mode string) bool {
	if len(reactions) > 128 {
		return false
	}
	seen := make(map[string]bool, len(reactions))
	for _, r := range reactions {
		key := r.Type + ":" + r.Value
		if seen[key] || !validReaction(r) || (mode == Delta && r.Count != 1) {
			return false
		}
		seen[key] = true
	}
	return true
}

func validReaction(r Reaction) bool {
	if r.Count < 0 || r.Count > 1_000_000_000 {
		return false
	}
	switch r.Type {
	case Emoji:
		return utf8.ValidString(r.Value) && strings.TrimSpace(r.Value) != "" && utf8.RuneCountInString(r.Value) <= 32
	case Custom:
		if r.Value == "" || len(r.Value) > 32 {
			return false
		}
		for _, char := range r.Value {
			if char < '0' || char > '9' {
				return false
			}
		}
		return true
	case Paid:
		return r.Value == ""
	default:
		return false
	}
}

// Package interaction owns chat-scoped temporary bot UI, never club permissions.
package interaction

import "time"

type Intent struct {
	Token             string
	ChatID            int64
	PanelID           int
	Kind, Action      string
	Revision, ActorID int64
	PromptID          int
	Clock, State      string
	Expires           time.Time
}

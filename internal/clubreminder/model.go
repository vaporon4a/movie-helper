// Package clubreminder owns monthly movie-discussion reminders and their text stock.
package clubreminder

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const Target = 5
const StockDays = 90
const DeliveryWindow = 6 * time.Hour
const PrimaryDeferrals = 3
const Policy = "club-reminder-v1"
const Explore = "explore"
const Discuss = "discuss"
const Ready = "ready"
const Sending = "sending"
const Sent = "sent"
const Unknown = "unknown"
const Retry = "retry"

var Tones = []string{"friendly", "dramatic", "sarcastic", "profane", "roast"}

type Settings struct {
	ChatID              int64
	Weekday, Ordinal    int
	Clock, Zone         string
	Enabled             bool
	Revision, Effective int64
	Stock               int
	LastAIError         string
	NextRefill          int64
	Issues              []Delivery
}
type Slot struct {
	At     time.Time
	Cycle  string
	Intent string
}
type Text struct {
	ID                                  int64
	Body, Intent, Tone, Provider, State string
}
type Delivery struct {
	ID, ChatID, Revision, Deadline int64
	SlotAt, NextAttempt            int64
	Intent, State, Error           string
	Text                           Text
}

func Hash(text string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(Normalize(text)))) }
func Normalize(text string) string {
	return strings.Join(strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) }), " ")
}
func ValidText(text Text) bool {
	words := len(strings.Fields(text.Body))
	if words < 60 || words > 240 || utf8.RuneCountInString(text.Body) > 2200 {
		return false
	}
	if text.Intent != Explore && text.Intent != Discuss {
		return false
	}
	tone := false
	for _, value := range Tones {
		tone = tone || value == text.Tone
	}
	if !tone || strings.ContainsAny(text.Body, "@<>*_`\x00\r") || strings.Contains(text.Body, "http") {
		return false
	}
	for _, r := range text.Body {
		if unicode.IsControl(r) && r != '\n' {
			return false
		}
	}
	return true
}
func Similar(first, second string) bool {
	a, b := shingles(Normalize(first)), shingles(Normalize(second))
	common := 0
	for key := range a {
		if b[key] {
			common++
		}
	}
	union := len(a) + len(b) - common
	return union > 0 && float64(common)/float64(union) >= 0.65
}
func shingles(text string) map[string]bool {
	words := strings.Fields(text)
	out := make(map[string]bool)
	for i := 0; i+2 < len(words); i++ {
		out[strings.Join(words[i:i+3], " ")] = true
	}
	return out
}

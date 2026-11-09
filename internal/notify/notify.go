// Package notify delivers notifications to the channels users set up
// (docs/decisions.md, "Notifications"). A Message is type-neutral: a title,
// a body, a severity, fields and a link. Each channel type has a Sender that
// checks and masks its own JSON settings and renders a Message in its own
// format. Discord (discord.go) and browsers (Web Push, webpush.go) exist;
// Slack, email or ntfy are a new Sender each, with no schema change.
package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Severity says how loudly a message is announced. What that means is up
// to the sender (Discord: @here or @everyone).
type Severity string

const (
	Normal    Severity = "normal"
	Emergency Severity = "emergency"
)

// Tone is the outcome a message reports, for colors.
type Tone string

const (
	ToneSuccess  Tone = "success"
	ToneFailure  Tone = "failure"
	ToneProgress Tone = "progress" // something started
	ToneNeutral  Tone = "neutral"  // cancelled, test
)

// Field is one labelled value (app, project, commit, ...).
type Field struct {
	Name   string
	Value  string
	Inline bool
}

// Message is one notification, before a sender renders it.
type Message struct {
	Event    string // event type, like deployment.failed
	Title    string // one line, like "shop: deployment 12 failed"
	Body     string // what happened, like the error message
	Hint     string // what to do about it (failures)
	Severity Severity
	Tone     Tone
	Fields   []Field
	URL      string // the dashboard page about it
	Tag      string // messages with the same tag are about one thing (an app)
	Time     time.Time
}

// Sender is one channel type.
type Sender interface {
	// Type is the channel type, like "discord".
	Type() string
	// Prepare checks the settings from the API and returns them as stored.
	// old is the stored settings of the channel being changed (nil for a new
	// one): a secret left out of in keeps its old value.
	Prepare(in map[string]any, old json.RawMessage) (json.RawMessage, error)
	// Public is the settings as the API shows them: secrets masked.
	Public(cfg json.RawMessage) map[string]any
	// Send delivers one message. It returns a *SendError for an answer of
	// the service that the user should see.
	Send(ctx context.Context, cfg json.RawMessage, m Message) error
}

// Registry maps a channel type to its sender.
type Registry map[string]Sender

// Register adds a sender.
func (r Registry) Register(s Sender) { r[s.Type()] = s }

// SendError is a delivery that failed with a reason the user can act on.
type SendError struct {
	Status int    // HTTP status of the service, 0 when it was not reached
	Msg    string // plain words, shown on the channel
	// Gone: the service says the destination no longer exists (a deleted
	// webhook, an expired browser subscription).
	Gone bool
}

func (e *SendError) Error() string { return e.Msg }

// ErrorText is the plain-words reason of a failed send, for the channel's
// "last message failed" line.
func ErrorText(err error) string {
	var se *SendError
	if errors.As(err, &se) {
		return se.Msg
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "The service did not answer in time."
	}
	if errors.Is(err, context.Canceled) {
		return "bitploy stopped before the message was sent."
	}
	if errors.Is(err, ErrBlockedAddress) {
		return "The address points to a private network, so bitploy does not send to it."
	}
	return fmt.Sprintf("Sending failed: %v", err)
}

// IsGone reports whether the destination no longer exists.
func IsGone(err error) bool {
	var se *SendError
	return errors.As(err, &se) && se.Gone
}

// truncate cuts s to at most n runes, ending with "…" when cut.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}

// str reads a string setting.
func str(in map[string]any, key string) (string, bool) {
	v, ok := in[key]
	if !ok || v == nil {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// sleepCtx waits d, or less when ctx ends; fn replaces it in tests.
func sleepCtx(ctx context.Context, fn func(context.Context, time.Duration) error, d time.Duration) error {
	if fn != nil {
		return fn(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Package reminders provides /remind: "/remind 10m tea" replies "⏰ tea" to
// the original message ten minutes later, even across restarts. It is the
// reference example of persistent timers.
package reminders

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// Config configures the module.
type Config struct {
	// MaxDelay caps how far ahead a reminder may be set; default 30 days.
	MaxDelay time.Duration `yaml:"max_delay"`
}

// Module handles /remind.
type Module struct{ max time.Duration }

// New returns the module.
func New(cfg Config) *Module {
	if cfg.MaxDelay <= 0 {
		cfg.MaxDelay = 30 * 24 * time.Hour
	}
	return &Module{max: cfg.MaxDelay}
}

// Descriptor declares the /remind command.
func (*Module) Descriptor() otogi.ModuleDescriptor {
	return otogi.ModuleDescriptor{ID: "reminders", Commands: []otogi.CommandSpec{
		{Name: "remind", Usage: "<delay> <text>", Description: "remind this chat after a delay such as 10m or 1h30m"},
	}}
}

// reminder is a timer's payload.
type reminder struct {
	To   otogi.Destination `json:"to"`
	Ref  otogi.EventRef    `json:"ref"`
	Text string            `json:"text"`
}

// Handle sets a timer for "/remind <delay> <text>".
func (m *Module) Handle(ctx context.Context, e otogi.Event, inv otogi.Invocation) error {
	delayArg, text, _ := strings.Cut(e.Command.Args, " ")
	delay, err := time.ParseDuration(delayArg)
	text = strings.TrimSpace(text)
	var reply string
	switch {
	case err != nil || delay <= 0 || text == "":
		reply = "Usage: /remind <delay> <text>, for example /remind 10m tea"
	case delay > m.max:
		reply = fmt.Sprintf("That is too far ahead; the limit is %s.", m.max)
	default:
		payload, err := json.Marshal(reminder{To: e.Destination(), Ref: e.Ref(), Text: text})
		if err != nil {
			return err
		}
		// The event ID keys the timer, so a replayed command sets it once.
		if err := inv.Host().SetTimer(ctx, string(e.ID), time.Now().Add(delay), payload); err != nil {
			return err
		}
		reply = fmt.Sprintf("⏰ I'll remind you in %s.", delay)
	}
	_, err = inv.Send(ctx, "ack", otogi.Reply(reply, e.Ref()))
	return err
}

// HandleTimer posts a due reminder as a reply to the original message.
func (*Module) HandleTimer(ctx context.Context, t otogi.Timer, inv otogi.Invocation) error {
	var r reminder
	if err := json.Unmarshal(t.Payload, &r); err != nil {
		return fmt.Errorf("decode reminder %q: %w", t.Key, err)
	}
	_, err := inv.SendTo(ctx, "remind", r.To, otogi.Reply("⏰ "+r.Text, r.Ref))
	return err
}

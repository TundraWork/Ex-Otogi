// Package reminders provides /remind: "/remind 10m tea" replies "⏰ tea" to
// the original message ten minutes later, even across restarts. It is the
// reference example of persistent timers, and of a module offering language
// model agents a tool (set_reminder).
package reminders

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// Config configures the module.
type Config struct {
	// MaxDelay caps how far ahead a reminder may be set; default 30 days.
	MaxDelay time.Duration `yaml:"max_delay"`
}

// Module handles /remind and offers set_reminder to agents.
type Module struct {
	max  time.Duration
	host otogi.Host
}

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
	},
		// The account's owner may use the command too; no reply starts with
		// a command prefix, so replies never trigger it.
		Filter:   otogi.Filter{Directions: []otogi.Direction{otogi.DirectionIncoming, otogi.DirectionOutgoing}},
		Provides: []string{llm.ToolsService("reminders")},
	}
}

// Start offers the module's tools to agents.
func (m *Module) Start(_ context.Context, host otogi.Host) error {
	m.host = host
	return host.Provide(llm.ToolsService("reminders"), []llm.Tool{llm.NewTool(llm.Function{
		Name:        "set_reminder",
		Description: "Set a reminder in this chat: after the delay, the reminder text is posted as a reply to the message being answered.",
		Parameters: json.RawMessage(`{"type": "object", "properties": {
			"delay": {"type": "string", "description": "how long from now, as a Go duration such as 10m, 1h30m, or 72h"},
			"text": {"type": "string", "description": "what to remind of, written to the people in the chat"}},
			"required": ["delay", "text"]}`),
	}, m.setReminder)})
}

// setReminder is the set_reminder tool. A replayed call sets the same timer
// again rather than a second one.
func (m *Module) setReminder(ctx context.Context, e otogi.Event, args json.RawMessage) (any, error) {
	var in struct{ Delay, Text string }
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("arguments: %w", err)
	}
	delay, err := time.ParseDuration(in.Delay)
	switch {
	case err != nil || delay <= 0:
		return nil, fmt.Errorf("delay %q: want a positive duration such as 10m or 1h30m", in.Delay)
	case delay > m.max:
		return nil, fmt.Errorf("delay %s: the limit is %s", delay, m.max)
	case strings.TrimSpace(in.Text) == "":
		return nil, fmt.Errorf("text is empty")
	}
	if err := m.remind(ctx, m.host, e, delay, strings.TrimSpace(in.Text), fmt.Sprintf("tool:%s:%x", e.ID, sha256.Sum256([]byte(in.Delay+"\x00"+in.Text)))); err != nil {
		return nil, err
	}
	return map[string]string{"due": time.Now().Add(delay).Format(time.RFC3339), "in": strings.TrimSpace(in.Delay)}, nil
}

// remind sets a reminder timer under key.
func (m *Module) remind(ctx context.Context, host otogi.Host, e otogi.Event, delay time.Duration, text, key string) error {
	payload, err := json.Marshal(reminder{To: e.Destination(), Ref: e.Ref(), Text: text})
	if err != nil {
		return err
	}
	return host.SetTimer(ctx, key, time.Now().Add(delay), payload)
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
		// The event ID keys the timer, so a replayed command sets it once.
		if err := m.remind(ctx, inv.Host(), e, delay, text, string(e.ID)); err != nil {
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

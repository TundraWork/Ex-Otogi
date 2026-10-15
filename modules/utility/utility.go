// Package utility provides everyday operator commands: /whoami for the IDs
// that allowlists and other modules' config need, /ping, /status, and /help.
package utility

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// Config configures the module.
type Config struct{}

// Status is the runtime summary /status shows; the composition root fills it.
type Status struct {
	Version  string
	Uptime   time.Duration
	Ready    bool
	Kernel   KernelStatus
	Drivers  []DriverStatus
	EventLog EventLogStatus
}

// KernelStatus summarizes the kernel.
type KernelStatus struct {
	State                          string
	InFlight, MaxInFlight, Workers int
}

// DriverStatus summarizes one driver.
type DriverStatus struct {
	Name   string // e.g. "telegram/main"
	Ready  bool
	Error  string
	Detail string // driver-specific, e.g. inbox counts
}

// EventLogStatus summarizes the event log.
type EventLogStatus struct {
	OK        bool
	LastError string
}

// CommandInfo describes one command of an enabled module, for /help.
type CommandInfo struct {
	Module otogi.ModuleID
	Spec   otogi.CommandSpec
}

// Deps are runtime views the composition root provides.
type Deps struct {
	// Operators are the platform-qualified actor IDs ("telegram:user:123")
	// allowed to use operator commands. The account's own messages always are.
	Operators []string
	// Status returns the current runtime status.
	Status func(ctx context.Context) Status
	// Commands lists the commands of every enabled module.
	Commands func() []CommandInfo
}

// Module implements the utility commands.
type Module struct{ deps Deps }

// New returns the module.
func New(_ Config, deps Deps) *Module { return &Module{deps: deps} }

// Descriptor declares the commands. Outgoing messages are admitted so the
// account's owner can use them; no reply starts with a command prefix, so a
// reply never invokes a command.
func (*Module) Descriptor() otogi.ModuleDescriptor {
	return otogi.ModuleDescriptor{
		ID: "utility",
		Commands: []otogi.CommandSpec{
			{Name: "whoami", Description: "show your ID and this chat's ID, for allowlists and module config"},
			{Name: "ping", Description: "check that the bot is responding"},
			{Name: "status", Description: "show the bot's runtime status (operators only)"},
			{Name: "help", Description: "list the available commands"},
		},
		Filter: otogi.Filter{Directions: []otogi.Direction{otogi.DirectionIncoming, otogi.DirectionOutgoing}},
	}
}

// Handle answers one command.
func (m *Module) Handle(ctx context.Context, e otogi.Event, inv otogi.Invocation) error {
	var reply string
	switch e.Command.Name {
	case "whoami":
		reply = whoami(e)
	case "ping":
		reply = "Pong."
		if !e.OccurredAt.IsZero() {
			reply = fmt.Sprintf("Pong. Delivered %s after it was sent.", time.Since(e.OccurredAt).Round(100*time.Millisecond))
		}
	case "status":
		if !IsOperator(e, m.deps.Operators) {
			reply = denied(e)
		} else {
			reply = formatStatus(m.deps.Status(ctx))
		}
	case "help":
		reply = formatHelp(m.deps.Commands())
	default:
		return nil
	}
	_, err := inv.Send(ctx, "reply", otogi.Reply(reply, e.Ref()))
	return err
}

// IsOperator reports whether e comes from the account itself or from an
// actor listed in operators.
func IsOperator(e otogi.Event, operators []string) bool {
	return e.Direction == otogi.DirectionOutgoing || (e.Actor.ID != "" && slices.Contains(operators, e.ActorKey()))
}

func denied(e otogi.Event) string {
	return fmt.Sprintf("Only operators can do that. Your ID is %s; an operator can add it to `operators` in the config.", e.ActorKey())
}

func whoami(e otogi.Event) string {
	var b strings.Builder
	if e.Actor.ID != "" {
		fmt.Fprintf(&b, "You: %s", e.ActorKey())
		if e.Actor.Name != "" {
			fmt.Fprintf(&b, " (%s)", e.Actor.Name)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "Chat: %s:%s", e.Platform, e.Conversation.ID)
	if e.Conversation.Kind != "" {
		fmt.Fprintf(&b, " (%s)", e.Conversation.Kind)
	}
	fmt.Fprintf(&b, "\nAccount: %s:%s", e.Platform, e.Account)
	if e.SourceID != "" {
		fmt.Fprintf(&b, "\nMessage: %s", e.SourceID)
	}
	return b.String()
}

func formatStatus(s Status) string {
	var b strings.Builder
	state := "ready"
	if !s.Ready {
		state = "NOT READY"
	}
	fmt.Fprintf(&b, "Status: %s, up %s\nVersion: %s\n", state, s.Uptime.Round(time.Second), s.Version)
	fmt.Fprintf(&b, "Kernel: %s, %d/%d events in flight, %d workers\n", s.Kernel.State, s.Kernel.InFlight, s.Kernel.MaxInFlight, s.Kernel.Workers)
	for _, d := range s.Drivers {
		ready := "ready"
		if !d.Ready {
			ready = "not ready"
		}
		fmt.Fprintf(&b, "Driver %s: %s", d.Name, ready)
		if d.Detail != "" {
			fmt.Fprintf(&b, ", %s", d.Detail)
		}
		if d.Error != "" {
			fmt.Fprintf(&b, "\n  error: %s", d.Error)
		}
		b.WriteString("\n")
	}
	if s.EventLog.OK {
		b.WriteString("Event log: ok")
	} else {
		fmt.Fprintf(&b, "Event log: write failing: %s", s.EventLog.LastError)
	}
	return b.String()
}

func formatHelp(commands []CommandInfo) string {
	if len(commands) == 0 {
		return "No commands are enabled."
	}
	slices.SortFunc(commands, func(a, b CommandInfo) int { return strings.Compare(a.Spec.Name, b.Spec.Name) })
	var b strings.Builder
	b.WriteString("Commands:")
	for _, c := range commands {
		fmt.Fprintf(&b, "\n/%s", c.Spec.Name)
		if c.Spec.Usage != "" {
			fmt.Fprintf(&b, " %s", c.Spec.Usage)
		}
		if c.Spec.Description != "" {
			fmt.Fprintf(&b, " — %s", c.Spec.Description)
		}
	}
	return b.String()
}

// Package chat answers people in chats with language model agents. Each
// agent profile answers in the chats it lists when a message starts with one
// of its keywords, or replies to a message it sent. See
// docs/superpowers/specs/2026-10-31-chat-module-design.md.
package chat

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/markdown"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// Module is the chat module.
type Module struct {
	cfg       Config
	profiles  []*profile
	byName    map[string]*profile
	operators []string
	host      otogi.Host
	store     *store
	now       func() time.Time

	mu     sync.RWMutex
	access map[string]map[string]bool // profile → chat → allowed, set with /chat; overrides the config
}

// New validates the configuration; providers finds the providers profiles
// name. operators may change where agents answer with /chat.
func New(cfg Config, providers func(string) (llm.Provider, error), operators []string) (*Module, error) {
	profiles, err := cfg.profiles(providers)
	if err != nil {
		return nil, err
	}
	m := &Module{cfg: cfg, profiles: profiles, byName: map[string]*profile{}, operators: operators, now: time.Now, access: map[string]map[string]bool{}}
	for _, p := range profiles {
		m.byName[p.Name] = p
	}
	return m, nil
}

// Descriptor declares the module. It sees every message of every direction,
// to keep the transcript of the chats it serves; only people's new messages
// trigger agents.
func (m *Module) Descriptor() otogi.ModuleDescriptor {
	var requires []string
	for _, p := range m.profiles {
		for _, g := range p.groups {
			if s := llm.ToolsService(g); !slices.Contains(requires, s) {
				requires = append(requires, s)
			}
		}
	}
	return otogi.ModuleDescriptor{
		ID:       "chat",
		Requires: requires,
		Commands: []otogi.CommandSpec{{
			Name: "chat", Usage: "list [chat] | allow <agent> [chat] | deny <agent> [chat]",
			Description: "show which agents answer in a chat, or (operators) change it",
		}},
		Subscribes: []otogi.EventAction{otogi.ActionMessageCreated, otogi.ActionMessageEdited, otogi.ActionMessageDeleted},
		Ordering:   otogi.OrderNone,
		Timeout:    m.cfg.Timeout,
		Filter:     otogi.Filter{Directions: []otogi.Direction{otogi.DirectionIncoming, otogi.DirectionOutgoing, otogi.DirectionSystem}},
	}
}

// Start opens the database and schedules pruning.
func (m *Module) Start(ctx context.Context, host otogi.Host) error {
	db, err := host.Database(ctx)
	if err != nil {
		return err
	}
	if m.store, err = openStore(ctx, db); err != nil {
		return err
	}
	if m.access, err = m.store.access(ctx); err != nil {
		return err
	}
	m.host = host
	for _, p := range m.profiles {
		p.tools = map[string]llm.Tool{}
		for _, g := range p.groups {
			tools, err := otogi.Lookup[[]llm.Tool](host, llm.ToolsService(g))
			if err != nil {
				return fmt.Errorf("profile %s: tools %s: %w", p.Name, g, err)
			}
			for _, t := range tools {
				name := t.Function().Name
				if _, dup := p.tools[name]; dup {
					return fmt.Errorf("profile %s: function %q is offered twice: %w", p.Name, name, otogi.ErrInvalid)
				}
				p.tools[name] = t
			}
		}
	}
	return host.Schedule("prune", "@every 1h", m.prune)
}

func (m *Module) prune(ctx context.Context, _ otogi.Invocation) error {
	n, err := m.store.prune(ctx, m.now().Add(-m.cfg.Retention))
	if err != nil {
		return err
	}
	m.host.Logger().Debug("chat: pruned", "rows", n)
	return nil
}

// Handle keeps the transcript of served chats and answers triggers.
func (m *Module) Handle(ctx context.Context, e otogi.Event, inv otogi.Invocation) error {
	if e.Command != nil && e.Command.Name == "chat" {
		return m.command(ctx, e, inv)
	}
	if e.Action == otogi.ActionMessageDeleted {
		return m.store.forget(ctx, e.Platform, e.Account, string(e.SourceID))
	}
	if !m.serves(chatKey(e)) {
		return nil
	}
	if err := m.store.record(ctx, e.Destination(), transcriptLine(e)); err != nil {
		return err
	}
	if e.Action != otogi.ActionMessageCreated || e.Direction != otogi.DirectionIncoming || e.Command != nil {
		return nil
	}
	p, parent, trigger, err := m.route(ctx, e)
	if err != nil || p == nil {
		return err
	}
	return m.respond(ctx, e, inv, p, parent, trigger)
}

// serves reports whether any profile answers in chat.
func (m *Module) serves(chat string) bool {
	for _, p := range m.profiles {
		if m.answers(p, chat) {
			return true
		}
	}
	return false
}

// route picks the agent a message triggers, and the turn it continues from
// (0 for a new thread). A reply to an agent continues its thread, unless the
// message starts with another agent's keyword, which starts a new one.
func (m *Module) route(ctx context.Context, e otogi.Event) (*profile, int64, string, error) {
	chat, text := chatKey(e), firstText(e.Message)
	var named *profile
	for _, p := range m.profiles {
		if m.answers(p, chat) && p.keyword(text) {
			named = p
			break
		}
	}
	if target, ok := replyTarget(e.Message); ok {
		turn, name, found, err := m.store.agentTurn(ctx, target)
		if err != nil {
			return nil, 0, "", err
		}
		if p := m.byName[name]; found && p != nil && m.answers(p, chat) && (named == nil || named == p) {
			return p, turn, "reply", nil
		}
	}
	if named != nil {
		return named, 0, "keyword", nil
	}
	if e.Mentioned {
		for _, p := range m.profiles {
			if m.answers(p, chat) && p.Mention {
				return p, 0, "mention", nil
			}
		}
	}
	return nil, 0, "", nil
}

func chatKey(e otogi.Event) string { return string(e.Platform) + ":" + string(e.Conversation.ID) }

func firstText(msg otogi.Message) string {
	for _, p := range msg.Parts {
		if p.Kind == otogi.PartText {
			return p.Text
		}
	}
	return ""
}

func replyTarget(msg otogi.Message) (otogi.EventRef, bool) {
	for _, r := range msg.Relations {
		if r.Kind == otogi.RelationReply {
			return r.Target, true
		}
	}
	return otogi.EventRef{}, false
}

// transcriptLine is how the transcript keeps a message: its text as
// Markdown and its media described, not downloaded.
func transcriptLine(e otogi.Event) line {
	l := line{SourceID: string(e.SourceID), At: e.OccurredAt, ActorID: string(e.Actor.ID), ActorName: speaker(e.Actor), Outgoing: e.Direction == otogi.DirectionOutgoing}
	if l.At.IsZero() {
		l.At = e.ReceivedAt
	}
	var texts []string
	for _, p := range e.Message.Parts {
		switch {
		case p.Kind == otogi.PartText:
			texts = append(texts, markdown.Format(p.Text, p.Annotations))
		case p.Media != nil:
			media := *p.Media
			media.Source.Inline = nil
			l.Media = append(l.Media, media)
		default:
			texts = append(texts, describe(p))
		}
	}
	l.Text = strings.Join(texts, "\n")
	if target, ok := replyTarget(e.Message); ok {
		l.ReplyTo = string(target.SourceEvent)
	}
	return l
}

func speaker(a otogi.Actor) string {
	if a.Name != "" {
		return a.Name
	}
	return string(a.ID)
}

// describe is a short text for a part a model cannot take as it is.
func describe(p otogi.Part) string {
	switch {
	case p.Location != nil:
		label := ""
		if p.Location.Label != "" {
			label = " " + p.Location.Label
		}
		return fmt.Sprintf("[location %.5f, %.5f%s]", p.Location.Latitude, p.Location.Longitude, label)
	case p.Contact != nil:
		return "[contact " + p.Contact.DisplayName + "]"
	case p.Poll != nil:
		return "[poll: " + p.Poll.Question + " — " + strings.Join(p.Poll.Options, " / ") + "]"
	case p.Media != nil:
		return "[" + mediaWord(*p.Media) + "]"
	}
	return "[" + string(p.Kind) + "]"
}

func mediaWord(m otogi.Media) string {
	word := string(m.Role)
	switch m.Role {
	case otogi.MediaImage:
		word = "photo"
	case "":
		word = "file"
	}
	if m.Duration > 0 {
		word += " " + m.Duration.Round(time.Second).String()
	}
	return word
}

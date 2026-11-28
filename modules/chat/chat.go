// Package chat answers people in chats with language model agents. Each
// agent profile answers in the chats on its allowlist when a message starts
// with one of its keywords, mentions the account, or replies to a message it
// sent; an agent judged by the decision service also answers group messages
// judged to be addressed to it, or to carry on an exchange it is in.
package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/decision"
	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/markdown"
	mediasvc "github.com/TundraWork/Ex-Otogi/pkg/media"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// Module is the chat module.
type Module struct {
	cfg         Config
	profiles    []*profile
	byName      map[string]*profile
	people      map[string]person // by platform:actor ID
	toolModules []otogi.ModuleID
	tools       map[otogi.ModuleID][]llm.Tool // each tool module's functions; set in Start
	host        otogi.Host
	store       *store
	decider     decision.Decider   // set in Start when an agent is judged
	describer   mediasvc.Describer // set in Start when an agent reads descriptions
	stickers    mediasvc.Stickers  // set in Start when an agent sends stickers
	operators   func() []string    // the bot's operators now, as platform:actor IDs
	now         func() time.Time

	// Waiting for senders to finish (see batch). Waits are timed on the
	// wall clock, not now.
	mu       sync.Mutex
	waits    map[string]*waiting    // by agent and chat
	inflight map[string][]*inflight // by chat
	together map[string]*judgeGroup // by chat
	replied  map[string][]time.Time // when agents started replies, read from now, by group chat
}

// New validates the configuration; providers finds the providers profiles
// name, and operators returns the bot's operators, as platform:actor IDs,
// whose names models see marked.
func New(cfg Config, providers func(string) (llm.Provider, error), operators func() []string) (*Module, error) {
	profiles, err := cfg.profiles(providers)
	toolModules, toolsErr := cfg.toolModules()
	people, peopleErr := cfg.people()
	if err := errors.Join(err, toolsErr, peopleErr); err != nil {
		return nil, err
	}
	m := &Module{cfg: cfg, profiles: profiles, byName: map[string]*profile{}, people: people, toolModules: toolModules, operators: operators, now: time.Now,
		waits: map[string]*waiting{}, inflight: map[string][]*inflight{}, together: map[string]*judgeGroup{}, replied: map[string][]time.Time{}}
	for _, p := range profiles {
		m.byName[p.Name] = p
	}
	return m, nil
}

// Descriptor declares the module. It sees every message of every direction
// (bots' too, when its "ignore bots" switch is off), to keep the transcript
// of the chats it serves; only people's new messages trigger agents. Each profile has an allowlist, named as the profile, and
// so does each module whose tools agents may use: the chats where they get
// them.
func (m *Module) Descriptor() otogi.ModuleDescriptor {
	var requires []string
	var lists []otogi.AllowlistSpec
	for _, p := range m.profiles {
		title := p.title()
		lists = append(lists, otogi.AllowlistSpec{Name: p.Name, Title: title, Description: title + " answers here."})
	}
	for _, t := range m.toolModules {
		requires = append(requires, llm.ToolsService(t))
		lists = append(lists, otogi.AllowlistSpec{Name: toolsList(t), Title: string(t) + " tools", Description: "Agents may use the " + string(t) + " module's tools here.", About: t})
	}
	if m.judged() {
		requires = append(requires, decision.Service)
	}
	if m.describes() {
		requires = append(requires, mediasvc.Service)
	}
	if m.sendsStickers() {
		requires = append(requires, mediasvc.StickersService)
	}
	return otogi.ModuleDescriptor{
		ID:         "chat",
		Requires:   requires,
		Subscribes: []otogi.EventAction{otogi.ActionMessageCreated, otogi.ActionMessageEdited, otogi.ActionMessageDeleted},
		Ordering:   otogi.OrderNone,
		Timeout:    time.Duration(m.cfg.Timeout),
		Filter:     otogi.Filter{Directions: []otogi.Direction{otogi.DirectionIncoming, otogi.DirectionOutgoing, otogi.DirectionSystem}},
		Allowlists: lists,
	}
}

// Start opens the database and schedules pruning, and the memory job when
// an agent keeps a memory.
func (m *Module) Start(ctx context.Context, host otogi.Host) error {
	db, err := host.Database(ctx)
	if err != nil {
		return err
	}
	if m.store, err = openStore(ctx, db); err != nil {
		return err
	}
	m.host = host
	m.tools = map[otogi.ModuleID][]llm.Tool{}
	names := map[string]otogi.ModuleID{}
	for _, module := range m.toolModules {
		tools, err := otogi.Lookup[[]llm.Tool](host, llm.ToolsService(module))
		if err != nil {
			return fmt.Errorf("tools %s: %w", module, err)
		}
		for _, t := range tools {
			name := t.Function().Name
			if other, dup := names[name]; dup {
				return fmt.Errorf("function %q is offered by both %s and %s: %w", name, other, module, otogi.ErrInvalid)
			}
			names[name] = module
		}
		m.tools[module] = tools
	}
	if m.judged() {
		if m.decider, err = otogi.Lookup[decision.Decider](host, decision.Service); err != nil {
			return fmt.Errorf("decision: %w", err)
		}
	}
	if m.describes() {
		if m.describer, err = otogi.Lookup[mediasvc.Describer](host, mediasvc.Service); err != nil {
			return fmt.Errorf("media: %w", err)
		}
	}
	if m.sendsStickers() || m.examines() {
		if m.stickers, err = otogi.Lookup[mediasvc.Stickers](host, mediasvc.StickersService); err != nil {
			return fmt.Errorf("media stickers: %w", err)
		}
	}
	if m.keepsMemory() {
		if err := host.Schedule("memory", memoryEvery, m.remember); err != nil {
			return err
		}
	}
	return host.Schedule("prune", "@every 1h", m.prune)
}

// judged reports whether any agent has the decision service judge messages:
// whether they address it or carry on its exchange, whether their senders
// have finished, whether it should look things up before answering, or
// whether a group reply is still to go out.
func (m *Module) judged() bool {
	for _, p := range m.profiles {
		if p.wake > 0 || p.follow > 0 || p.chime > 0 || p.waitMax > 0 || p.lookup > 0 || p.stale > 0 {
			return true
		}
	}
	return false
}

func (m *Module) prune(ctx context.Context, _ otogi.Invocation) error {
	n, err := m.store.prune(ctx, m.now().Add(-time.Duration(m.cfg.Retention)))
	if err != nil {
		return err
	}
	m.host.Logger().Debug("chat: pruned", "rows", n)
	return nil
}

// Handle keeps the transcript of served chats and answers triggers.
func (m *Module) Handle(ctx context.Context, e otogi.Event, inv otogi.Invocation) error {
	if e.Action == otogi.ActionMessageDeleted {
		return m.store.forget(ctx, e.Platform, e.Account, string(e.SourceID))
	}
	if !m.serves(e.ChatKey()) {
		return nil
	}
	if err := m.store.record(ctx, e.Destination(), transcriptLine(e)); err != nil {
		return err
	}
	if e.Action != otogi.ActionMessageCreated || e.Direction != otogi.DirectionIncoming || e.Command != nil || e.Actor.Bot {
		return nil
	}
	// The judgment and then the reply wait for this message's descriptions
	// until a deadline each, the media of the message together; see
	// DescribeWait.
	at := time.Now()
	judgeUntil, replyUntil := at.Add(time.Duration(m.cfg.DescribeWait.Judge)), at.Add(time.Duration(m.cfg.DescribeWait.Reply))
	p, parent, trigger, elsewhere, err := m.route(ctx, e)
	if err != nil || p == nil && elsewhere {
		return err
	}
	if p != nil {
		if !waits(p, e) {
			return m.answer(ctx, inv, ask{p: p, msgs: []otogi.Event{e}, parent: parent, trigger: trigger}, replyUntil)
		}
		release := m.judging(e.ChatKey(), at)
		defer release() // however this ends, a panic included
		done, known, err := m.doneOf(ctx, e, p, judgeUntil)
		if err != nil {
			return err
		}
		return m.join(ctx, inv, p, entry{e: e, at: at, parent: parent, trigger: trigger}, done, known, release)
	}
	window := m.window(e)
	release := func() {}
	if window > 0 {
		release = m.judging(e.ChatKey(), at)
		defer release()
	}
	v, err := m.judgeTogether(ctx, e, judgeUntil, window)
	if err != nil {
		return err
	}
	// A message that does not call an agent waiting to speak up tells it
	// whether to go on waiting, before this message stops being judged:
	// the wait, when due, waits for that.
	for p, s := range v.still {
		if p == v.p {
			continue // it calls the agent: it joins the batch
		}
		r, _, err := m.rhythmOf(ctx, e)
		if err != nil {
			return err
		}
		m.heard(p, e.ChatKey(), at, s, r)
	}
	if v.p == nil {
		return nil
	}
	if !waits(v.p, e) {
		release()
		return m.answer(ctx, inv, ask{p: v.p, msgs: []otogi.Event{e}, trigger: v.trigger, j: v.j}, replyUntil)
	}
	return m.join(ctx, inv, v.p, entry{e: e, at: at, trigger: v.trigger, j: v.j}, v.done, v.doneKnown, release)
}

// answer has an agent answer at once.
func (m *Module) answer(ctx context.Context, inv otogi.Invocation, a ask, until time.Time) error {
	_, err := m.respond(ctx, inv, a, until)
	return err
}

// join puts en into p's batch in its chat, and, when en opened it, waits for
// it and answers it; release ends en's being judged, once it is in (the
// caller defers it too, for when join ends before).
func (m *Module) join(ctx context.Context, inv otogi.Invocation, p *profile, en entry, done float64, known bool, release func()) error {
	r, own, err := m.rhythmOf(ctx, en.e)
	if err != nil {
		return err
	}
	b := m.enter(p, en, done, known, r, own)
	release()
	if b == nil {
		return nil
	}
	return m.run(ctx, inv, b)
}

// toolsIn is the functions agents get in chat: those of the modules whose
// tools lists have it.
func (m *Module) toolsIn(chat string) map[string]llm.Tool {
	out := map[string]llm.Tool{}
	for _, module := range m.toolModules {
		if m.host.Allowed(toolsList(module), chat) {
			for _, t := range m.tools[module] {
				out[t.Function().Name] = t
			}
		}
	}
	return out
}

// answers reports whether p answers in chat: whether chat is on p's
// allowlist.
func (m *Module) answers(p *profile, chat string) bool { return m.host.Allowed(p.Name, chat) }

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
// message starts with another agent's keyword, which starts a new one. A
// reply to a message the account sent for another module, such as a card, is
// no mention, though platforms flag it as one: it addresses no agent, and
// elsewhere reports it so the message is not judged either. In a group, a
// keyword alone does not trigger an agent whose judgment wakes it: starting
// with a name is not always talking to it, so the judgment decides; with a
// mention, the keyword still picks the agent.
func (m *Module) route(ctx context.Context, e otogi.Event) (*profile, int64, string, bool, error) {
	chat, text := e.ChatKey(), firstText(e.Message)
	var named *profile
	for _, p := range m.profiles {
		if m.answers(p, chat) && p.keyword(text) {
			named = p
			break
		}
	}
	mentioned, elsewhere := e.Mentioned, false
	if target, ok := replyTarget(e.Message); ok {
		turn, name, _, found, err := m.store.agentTurn(ctx, target)
		if err != nil {
			return nil, 0, "", false, err
		}
		if p := m.byName[name]; found && p != nil && m.answers(p, chat) && (named == nil || named == p) {
			return p, turn, "reply", false, nil
		}
		if !found {
			to, kept, err := m.store.message(ctx, e.Destination(), string(target.SourceEvent))
			if err != nil {
				return nil, 0, "", false, err
			}
			if kept && to.Outgoing {
				mentioned, elsewhere = false, true
			}
		}
	}
	if named != nil && (mentioned || !judgesNames(named, e)) {
		return named, 0, "keyword", false, nil
	}
	if mentioned {
		for _, p := range m.profiles {
			if m.answers(p, chat) && p.Mention {
				return p, 0, "mention", false, nil
			}
		}
	}
	// A reply to a card that starts with an agent's name may still be talking
	// to the agent: the judgment decides, as the keyword would have.
	return nil, 0, "", elsewhere && named == nil, nil
}

// judgesNames reports whether p's keywords are left to the judgment in e's
// chat: a group, for an agent whose judgment wakes it.
func judgesNames(p *profile, e otogi.Event) bool {
	return p.wake > 0 && e.Conversation.Kind == otogi.ConversationGroup
}

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
	l := line{SourceID: string(e.SourceID), At: e.OccurredAt, ActorID: string(e.Actor.ID), ActorName: speaker(e.Actor), ActorRole: string(e.Actor.Role), Outgoing: e.Direction == otogi.DirectionOutgoing}
	if l.At.IsZero() {
		l.At = e.ReceivedAt
	}
	var texts []string
	for _, p := range e.Message.Parts {
		switch {
		case p.Kind == otogi.PartText:
			texts = append(texts, markdown.Format(nameOnly(p.Text, p.Annotations)))
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

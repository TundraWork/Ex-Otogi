package chat

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/decision"
	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// coveredThreshold is how sure the judgment must be that the messages to an
// agent that came while it replied need no answer of their own. It is Eku's
// live value for the same question (2026-11-25), with no replay on Otogi
// behind it: records with skipped: covered show what it catches, to tune it
// by once it runs.
const coveredThreshold = 0.7

// A batch is what an agent waiting for senders to finish answers together
// in a group: the messages to it from the first on, while more may come. At
// most one batch per agent and chat waits at a time; messages to the agent
// while it replies wait in the next one, opened by the first of them. The
// message that opens a batch waits in its own Handle and answers from it, so
// the answer is a reply to a message being handled (a passive reply on QQ),
// and a restart handles that message again; messages that join return.
type batch struct {
	key     string
	p       *profile
	entries []entry
	first   time.Time // when the first message arrived
	// deadline is when to answer: after the latest message, by how sure it
	// is that its sender has finished, but no later than p.waitMax after the
	// first.
	deadline time.Time
	changed  chan struct{} // signalled when a message joins
	// after is the reply running when the batch opened: it waits for it to
	// end. Nil when none was.
	after chan struct{}
}

// entry is a message in a batch.
type entry struct {
	e       otogi.Event
	at      time.Time // when it arrived
	parent  int64     // the turn it continues, 0 for a new thread
	trigger string
	j       *judgment
}

// waiting is the state of one agent in one chat, under Module.mu.
type waiting struct {
	open     *batch        // the batch gathering messages, if any
	replying chan struct{} // set while the agent replies, closed when it is done
	last     spoken        // the agent's last reply, for covered
}

// spoken is a reply of an agent: when it ran, on the wall clock, and what it
// showed. A message that arrived while it ran came during it, however late
// its judgment came back.
type spoken struct {
	from, to time.Time
	text     string
}

// during reports whether a message that arrived at came during s.
func (s spoken) during(at time.Time) bool {
	return s.text != "" && !at.Before(s.from) && at.Before(s.to)
}

// inflight is a message of a chat whose judgment has not come back: a batch
// due after it arrived waits for it, as it may join.
type inflight struct {
	at   time.Time
	done chan struct{}
}

// judgeGroup is the messages of a chat judged in one request: those that
// arrive within window of the first.
type judgeGroup struct {
	events   []otogi.Event
	until    time.Time
	done     chan struct{} // closed when verdicts are in
	verdicts []verdict
	err      error
}

// window is how long the first message of e's group chat waits for others to
// be judged with it: the shortest wait.min of the agents there that wait,
// since they wait at least that long anyway. 0 judges each message alone.
func (m *Module) window(e otogi.Event) time.Duration {
	if e.Conversation.Kind != otogi.ConversationGroup {
		return 0
	}
	var w time.Duration
	for _, p := range m.profiles {
		if p.waitMax > 0 && m.answers(p, e.ChatKey()) && (w == 0 || p.waitMin < w) {
			w = p.waitMin
		}
	}
	return w
}

// judgeTogether judges e with the other messages of its chat that arrive
// within window of the first of them, in one request: the first sends it
// after window, the others join it. Each waits for the request and takes its
// own verdict.
func (m *Module) judgeTogether(ctx context.Context, e otogi.Event, until time.Time, window time.Duration) (verdict, error) {
	if window <= 0 {
		v, err := m.judge(ctx, []otogi.Event{e}, until)
		if err != nil {
			return verdict{}, err
		}
		return v[0], nil
	}
	chat := e.ChatKey()
	m.mu.Lock()
	g, first := m.together[chat], false
	if g == nil {
		g, first = &judgeGroup{done: make(chan struct{})}, true
		m.together[chat] = g
	}
	i := len(g.events)
	g.events = append(g.events, e)
	if until.After(g.until) {
		g.until = until
	}
	m.mu.Unlock()
	if first {
		m.sendTogether(ctx, chat, g, window)
	}
	select {
	case <-g.done:
	case <-ctx.Done():
		return verdict{}, ctx.Err()
	}
	if g.err != nil {
		return verdict{}, g.err
	}
	return g.verdicts[i], nil
}

// errUnjudged is what the messages of a request that ended without an answer
// get: the message that sent it panicked.
var errUnjudged = errors.New("chat: the request judging this message ended without an answer")

// sendTogether sends g's request after window, as the first of its messages.
// g is done however that ends, a panic included, so the other messages never
// wait on a request that is not coming, and the chat's next messages start a
// request of their own.
func (m *Module) sendTogether(ctx context.Context, chat string, g *judgeGroup, window time.Duration) {
	g.err = errUnjudged
	defer func() {
		m.mu.Lock()
		if m.together[chat] == g {
			delete(m.together, chat)
		}
		m.mu.Unlock()
		close(g.done)
	}()
	timer := time.NewTimer(window)
	select {
	case <-timer.C:
	case <-ctx.Done():
		timer.Stop()
	}
	m.mu.Lock()
	delete(m.together, chat)
	es, until := g.events, g.until
	m.mu.Unlock()
	var verdicts []verdict
	err := ctx.Err()
	if err == nil {
		verdicts, err = m.judge(ctx, es, until)
	}
	g.verdicts, g.err = verdicts, err
}

// waits reports whether p waits for senders to finish in e's chat.
func waits(p *profile, e otogi.Event) bool {
	return p.waitMax > 0 && e.Conversation.Kind == otogi.ConversationGroup
}

// judging marks a message of chat as being judged, until the returned
// function is first called: a Handle defers it, so a panic ends the mark
// too, and calls it once the message is in a batch.
func (m *Module) judging(chat string, at time.Time) func() {
	f := &inflight{at: at, done: make(chan struct{})}
	m.mu.Lock()
	m.inflight[chat] = append(m.inflight[chat], f)
	m.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			m.mu.Lock()
			m.inflight[chat] = slices.DeleteFunc(m.inflight[chat], func(g *inflight) bool { return g == f })
			if len(m.inflight[chat]) == 0 {
				delete(m.inflight, chat)
			}
			m.mu.Unlock()
			close(f.done)
		})
	}
}

// awaitJudging waits for the messages of chat that arrived before due and
// are still being judged, and reports whether there were any.
func (m *Module) awaitJudging(ctx context.Context, chat string, due time.Time) bool {
	m.mu.Lock()
	var pending []*inflight
	for _, f := range m.inflight[chat] {
		if f.at.Before(due) {
			pending = append(pending, f)
		}
	}
	m.mu.Unlock()
	for _, f := range pending {
		select {
		case <-f.done:
		case <-ctx.Done():
			return true
		}
	}
	return len(pending) > 0
}

// rhythmOf is how fast e's sender types, from the chat's recent messages and
// agents' replies there, and whether that is their own rhythm.
func (m *Module) rhythmOf(ctx context.Context, e otogi.Event) (time.Duration, bool, error) {
	lines, err := m.store.recent(ctx, e.Destination(), m.now().Add(time.Millisecond), rhythmLines)
	if err != nil {
		return 0, false, err
	}
	m.mu.Lock()
	replied := slices.Clone(m.replied[e.ChatKey()])
	m.mu.Unlock()
	r, own := rhythm(lines, replied, string(e.Actor.ID))
	return r, own, nil
}

// startedReplying notes that an agent started a reply in e's chat at at, for
// the rhythm of its senders: the last rhythmLines replies of each group.
func (m *Module) startedReplying(e otogi.Event, at time.Time) {
	if e.Conversation.Kind != otogi.ConversationGroup {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	chat := e.ChatKey()
	m.replied[chat] = append(m.replied[chat], at)
	if n := len(m.replied[chat]); n > rhythmLines {
		m.replied[chat] = slices.Delete(m.replied[chat], 0, n-rhythmLines)
	}
}

// enter puts en, a message to en.p's agent, into the agent's batch in its
// chat, and returns the batch when en opened it: the caller then runs it.
// done and known are how sure it is that the sender has finished; r is
// their rhythm, and own whether it is theirs.
func (m *Module) enter(p *profile, en entry, done float64, known bool, r time.Duration, own bool) *batch {
	key := p.Name + "\x00" + en.e.ChatKey()
	wait := waitFor(p.waitMin, done, known, r)
	if en.trigger == "follow" {
		wait = followUpWait(wait, r, own)
	}
	due := en.at.Add(wait)
	m.mu.Lock()
	defer m.mu.Unlock()
	w := m.waits[key]
	if w == nil {
		w = &waiting{}
		m.waits[key] = w
	}
	if b := w.open; b != nil {
		// Messages judged together reach here in no particular order.
		b.entries = append(b.entries, en)
		if en.at.Before(b.first) {
			b.first = en.at
		}
		b.deadline = minTime(due, b.first.Add(p.waitMax))
		select {
		case b.changed <- struct{}{}:
		default:
		}
		return nil
	}
	b := &batch{key: key, p: p, entries: []entry{en}, first: en.at, deadline: minTime(due, en.at.Add(p.waitMax)), changed: make(chan struct{}, 1), after: w.replying}
	w.open = b
	return b
}

func minTime(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}

// run waits for b to be due and answers it. A batch that opened during a
// reply first waits for that reply. When b is due, messages of the chat
// still being judged that arrived before then are waited for, since they may
// join. Messages that arrived during the previous reply are judged against
// it first, and left out when it already did for them.
func (m *Module) run(ctx context.Context, inv otogi.Invocation, b *batch) error {
	// Ending before b closes, a panic included, drops it, so the agent's
	// next messages open a batch of their own; once it closed, this does
	// nothing.
	defer m.drop(b)
	chat := b.entries[0].e.ChatKey()
	if b.after != nil {
		select {
		case <-b.after:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	for {
		m.mu.Lock()
		due := b.deadline
		m.mu.Unlock()
		if wait := time.Until(due); wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-timer.C:
			case <-b.changed:
				timer.Stop()
				continue
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			}
		}
		if m.awaitJudging(ctx, chat, due) {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			continue
		}
		m.mu.Lock()
		if time.Now().Before(b.deadline) { // a message joined meanwhile
			m.mu.Unlock()
			continue
		}
		break
	}
	// Still under m.mu: b closes and the agent starts replying. w stays
	// after the reply: a message that arrived during it may come in only
	// once its judgment is back.
	w := m.waits[b.key]
	w.open = nil
	done := make(chan struct{})
	w.replying = done
	entries, previous := b.entries, w.last
	w.last = spoken{from: time.Now()}
	m.mu.Unlock()
	waited := time.Since(b.first)

	// The reply ends however answerBatch does, a panic included: one left
	// running would hold every later batch of the agent here.
	var text string
	defer func() {
		m.mu.Lock()
		w.replying = nil
		w.last.to, w.last.text = time.Now(), text
		m.mu.Unlock()
		close(done)
	}()
	var err error
	text, err = m.answerBatch(ctx, inv, b.p, entries, previous, waited)
	return err
}

// drop forgets b if it is still open: the Handle running it ended before it
// closed, and it will not be answered.
func (m *Module) drop(b *batch) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if w := m.waits[b.key]; w != nil && w.open == b {
		w.open = nil
	}
}

// answerBatch has p answer entries, after leaving out those that arrived
// during the previous reply when that reply already did for them; it returns
// what the agent said.
func (m *Module) answerBatch(ctx context.Context, inv otogi.Invocation, p *profile, entries []entry, previous spoken, waited time.Duration) (string, error) {
	entries = slices.Clone(entries)
	slices.SortStableFunc(entries, func(a, b entry) int { return a.at.Compare(b.at) })
	info := &batchInfo{waited: waited}
	during := func(en entry) bool { return previous.during(en.at) }
	if slices.ContainsFunc(entries, during) {
		covered, err := m.covered(ctx, p, entries, previous.text, during)
		if err != nil {
			return "", err
		}
		if covered {
			for _, en := range entries {
				if during(en) {
					info.covered = append(info.covered, string(en.e.SourceID))
				}
			}
			if rest := slices.DeleteFunc(slices.Clone(entries), during); len(rest) > 0 {
				entries = rest
			} else {
				m.record(ctx, askOf(p, entries, info), nil, llm.RunResult{}, nil, nil, nil, 0, "covered")
				return "", nil
			}
		}
	}
	for _, en := range entries {
		info.messages = append(info.messages, string(en.e.SourceID))
	}
	until := entries[len(entries)-1].at.Add(describeWait)
	return m.respond(ctx, inv, askOf(p, entries, info), until)
}

// askOf is what p answers in a batch of entries: the first's thread, and the
// trigger and judgment of the first that called the agent, or of the first
// when all only carried on its exchange. A batch in which anyone called it
// was asked for, and is no follow-up.
func askOf(p *profile, entries []entry, info *batchInfo) ask {
	lead := entries[0]
	if i := slices.IndexFunc(entries, func(en entry) bool { return en.trigger != "follow" }); i >= 0 {
		lead = entries[i]
	}
	a := ask{p: p, parent: entries[0].parent, trigger: lead.trigger, j: lead.j, batch: info}
	for _, en := range entries {
		a.msgs = append(a.msgs, en.e)
	}
	return a
}

// briefState is what the request before a reply is about: the recent chat
// up to the batch, the agents present, and the batch; reply is the agent's
// previous reply and the batch's messages that came during it.
type (
	briefState struct {
		Chat    string       `json:"chat"`
		Agents  []judgeAgent `json:"agents"`
		History []judgeLine  `json:"history"`
		Batch   []string     `json:"batch"`
		Reply   *briefReply  `json:"reply,omitempty"`
	}
	briefReply struct {
		Text   string   `json:"text"`
		During []string `json:"during"`
	}
)

// covered asks whether p's previous reply already did for the messages of
// entries that came during it. A failed request is no decision: they are
// answered.
func (m *Module) covered(ctx context.Context, p *profile, entries []entry, previous string, during func(entry) bool) (bool, error) {
	es := make([]otogi.Event, 0, len(entries))
	for _, en := range entries {
		es = append(es, en.e)
	}
	var present []*profile
	for _, q := range m.profiles {
		if m.answers(q, es[0].ChatKey()) {
			present = append(present, q)
		}
	}
	state, msgs, err := m.judgeState(ctx, es, present, p.Media != nil)
	if err != nil {
		return false, err
	}
	brief := briefState{Chat: state.Chat, Agents: state.Agents, History: state.History, Batch: msgs, Reply: &briefReply{Text: previous}}
	for i, en := range entries {
		if during(en) {
			brief.Reply.During = append(brief.Reply.During, msgs[i])
		}
	}
	answers, err := m.decider.Decide(ctx, decision.Request{State: brief, Questions: map[string]decision.Question{coveredID: coveredQuestion()}})
	if err != nil {
		return false, nil
	}
	return answers[coveredID] >= coveredThreshold, nil
}

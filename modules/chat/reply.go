package chat

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// cleanupTimeout bounds finishing a reply after its context ended.
const cleanupTimeout = 30 * time.Second

// followNote tells the model why it speaks on a follow-up. It goes with the
// messages, not the system prompt: on Eku, a "keep it short" left only in
// the system prompt let replies grow from 31 to 60 characters on average.
const followNote = "(Nobody named you: someone is carrying on the exchange with you. Keep it short.)"

// chimeNote tells the model why it speaks when it chimes in, as followNote
// does on a follow-up.
const chimeNote = "(Nobody asked you; you chose to join in. Keep it short.)"

// lookupNote ends a turn judged to need looking up: answered from memory,
// such questions came out wrong on Eku. It goes in the turn, not the system
// prompt, where Eku kept to such notes far less.
const lookupNote = "(Look this up online before answering.)"

// ask is what an agent answers: a message, or the batch of messages to it
// it waited for (see batch), in the order they arrived.
type ask struct {
	p       *profile
	msgs    []otogi.Event
	parent  int64 // the turn the first continues, 0 for a new thread
	trigger string
	j       *judgment   // the judgment that woke p for the first, if one did
	batch   *batchInfo  // nil when p did not wait
	lookup  *briefed    // what the request before the reply said of looking things up; nil when not asked yet
	stale   *staleCheck // what the check before the reply went out said; nil when it asked nothing
	hidden  int         // how many stickers the agent is given past its list
}

// batchInfo is what a reply's record says of its batch.
type batchInfo struct {
	messages []string // the source IDs of the messages it answers
	waited   time.Duration
	covered  []string // those left out as the previous reply did for them
	// still is, for a wait to speak up the agent gave up, how sure the
	// judgment was that it still meant to; nil for a gave-up wait whose
	// message left the recent chat, and for every other batch.
	still *float64
}

// respond has a.p answer a.msgs, continuing from turn a.parent (0 for a new
// thread): the reply streams into the chat, and the turns are kept. What the
// model first says before calling tools goes out as its own message before
// the tools run, so the chat sees it at once even where nothing streams (QQ
// groups); the rest of the reply, words before later tools included, follows
// in one more message. The reply replies to the last message of the batch.
// until is how long the messages may wait for their media's descriptions. It
// returns what the reply showed.
func (m *Module) respond(ctx context.Context, inv otogi.Invocation, a ask, until time.Time) (string, error) {
	started := m.now()
	p, e, last := a.p, a.msgs[0], a.msgs[len(a.msgs)-1]
	m.startedReplying(e, started)
	quote, err := m.quotes(ctx, a, started)
	if err != nil {
		return "", err
	}
	path, err := m.thread(ctx, a.parent, p.MaxTurns)
	if err != nil {
		return "", err
	}
	content, named, inBatch, err := m.askContent(ctx, inv, a, until)
	if err != nil {
		return "", err
	}
	user := llm.Turn{Role: llm.RoleUser, Content: content}
	// What leads the message, the notes on the people in it and the recent
	// chat, goes in this request only: the thread keeps the message alone.
	sent := llm.Turn{Role: llm.RoleUser, Content: user.Content}
	var recent string
	if p.context > 0 {
		var inRecent []sender
		if recent, inRecent, err = m.contextText(ctx, a, path, inBatch, started); err != nil {
			return "", err
		}
		named = append(inRecent, named...)
	}
	// The stickers the agent may send follow its system prompt: they change
	// seldom, so they are cached with it.
	picks := m.stickersFor(ctx, p)
	stickerList := ""
	if picks != nil {
		stickerList, a.hidden = picks.text(), picks.hidden
	}
	var lead []string
	if s := m.aboutPeople(e.Platform, named); s != "" {
		lead = append(lead, s)
	}
	if s := m.answerLead(a, recent); s != "" {
		lead = append(lead, s)
	}
	if len(lead) > 0 {
		sent.Content = llm.Join(otogi.TextMessage(strings.Join(lead, "\n\n")), user.Content)
	}
	if note := p.note(a.trigger); note != "" {
		sent.Content = llm.Join(sent.Content, otogi.TextMessage(note))
	}
	if a.lookup == nil && p.lookup > 0 {
		if _, a.lookup, err = m.briefEvents(ctx, p, a.msgs, func(int) bool { return false }, ""); err != nil {
			return "", err
		}
	}
	if a.lookup.look() {
		sent.Content = llm.Join(sent.Content, otogi.TextMessage(lookupNote))
	}
	marked := hasRoleMark(sent.Content) || slices.ContainsFunc(path, func(t storedTurn) bool { return hasRoleMark(t.Turn.Content) })
	req := llm.Request{Model: p.Model, System: m.system(p, e, stickerList, marked), Thinking: p.Thinking, MaxOutput: p.MaxOutput, Temperature: p.Temperature, Tools: p.builtins, Plain: p.PlainText}
	for _, t := range path {
		req.Turns = append(req.Turns, t.Turn)
	}
	req.Turns = append(req.Turns, sent)
	tools := m.toolsIn(e.ChatKey())
	if picks != nil {
		tools[stickerTool] = picks.tool()
	}
	var silent silence
	if p.Silent {
		tools[silentTool] = silent.tool()
	}
	if p.examine > 0 {
		tools[examineTool] = (&looker{m: m, a: a, inv: inv, path: path, until: started}).tool()
	}

	// The reply is at most two messages, each a stream of its own: "reply",
	// and "reply-2" for what follows words said before tools. The second is
	// opened only once it has something to show, so no empty message is
	// sent or recorded.
	//
	// A reply checked before it goes out (stale.go) is checked once, before
	// its first message, and does not stream, so nothing shows before.
	hold, checked := checksStale(a), false
	passes := func(ctx context.Context, draft string) bool {
		if checked {
			return true
		}
		checked = true
		if a.stale = m.staleOf(ctx, a, started, draft); a.stale.holds() {
			a.stale.Draft = draft
			return false
		}
		return true
	}
	var refs []otogi.EventRef
	var shownText []string // what the reply showed, for the next batch's covered
	stream, said := inv.Stream(ctx, "reply", e.Destination()), false
	update := func(msg otogi.Message) error {
		if len(msg.Parts) == 0 || hold {
			return nil
		}
		if stream == nil {
			stream = inv.Stream(ctx, "reply-2", e.Destination())
		}
		return stream.Update(ctx, present(p, last, msg, quote))
	}
	say := func(msg otogi.Message) error {
		if !passes(ctx, msg.PlainText()) {
			return errStale
		}
		sent, err := stream.Close(ctx, present(p, last, msg, quote))
		refs, stream, said = append(refs, sent...), nil, true
		shownText = append(shownText, msg.PlainText())
		return err
	}
	out, runErr := llm.Run(llm.ContextWithAgent(ctx, p.Name), p.provider, req, tools, last, llm.Limits{Rounds: p.MaxToolRounds, Reserve: *m.cfg.ToolReserve},
		update, say, func(c llm.CallRecord) { m.recordCall(ctx, last, p, c) })

	// The reply is finished even when ctx ended, so the chat is not left
	// with a partial answer.
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()
	// A picked sticker follows the reply, unless the reply failed.
	var sticker *sentSticker
	if picks != nil && picks.picked != nil {
		sticker = &sentSticker{ID: picks.picked.ID, Text: picks.picked.Text, Listed: picks.listed}
	}
	sends := sticker != nil && runErr == nil
	held := errors.Is(runErr, errStale)
	// The agent replied nothing: it called stay_silent, said nothing, and
	// picked no sticker.
	quiet := runErr == nil && silent.called && !said && len(out.Last.Parts) == 0 && !sends
	notes := p.FailureNotes != "direct_chats" || e.Conversation.Kind == otogi.ConversationDirect
	var final otogi.Message
	if !held && !quiet {
		final = ending(out, said || sends, runErr, p, notes)
		if !said && (len(final.Parts) > 0 || sends) {
			draft := final.PlainText()
			if sends {
				draft = strings.TrimSpace(draft + "\n[sticker: " + sticker.Text + "]")
			}
			held = !passes(cleanup, draft)
		}
	}
	// The model gave no answer, and no note says so here.
	unanswered := !held && !quiet && runErr == nil && !said && !sends && len(final.Parts) == 0
	if held || quiet || unanswered {
		// Nothing goes out: the stream closes empty, which shows nothing,
		// and the turn is not kept.
		if stream != nil {
			_, _ = stream.Close(cleanup, otogi.Message{})
		}
		skipped := "stale"
		switch {
		case quiet:
			skipped = "silent"
		case unanswered:
			skipped = "no_answer"
		}
		m.record(cleanup, a, nil, out, nil, nil, nil, m.now().Sub(started), skipped)
		return "", nil
	}
	var closeErr error
	if stream != nil || len(final.Parts) > 0 {
		if stream == nil {
			stream = inv.Stream(cleanup, "reply-2", e.Destination())
		}
		// A reply that is only a sticker closes its stream empty, which
		// shows nothing: not even the agent's label.
		shown := otogi.Message{}
		if len(final.Parts) > 0 {
			shown = present(p, last, final, quote)
			shownText = append(shownText, final.PlainText())
		}
		var closed []otogi.EventRef
		closed, closeErr = stream.Close(cleanup, shown)
		refs = append(refs, closed...)
	}
	messages := make([]sentMessage, 0, len(refs)+1)
	for _, ref := range refs {
		messages = append(messages, sentMessage{Ref: ref})
	}
	var stickerErr error
	if sends {
		var ref otogi.EventRef
		ref, stickerErr = m.sendSticker(cleanup, last, inv, *picks.picked, quote)
		if stickerErr == nil {
			sticker.Ref = string(ref.SourceEvent)
			refs = append(refs, ref)
			messages = append(messages, sentMessage{Ref: ref, Sticker: sticker.Text})
		}
	}
	var ids []int64
	var storeErr error
	if runErr == nil {
		turns := []storedTurn{{Turn: user, SourceID: string(e.SourceID)}}
		for _, t := range out.Turns {
			turns = append(turns, storedTurn{Turn: t})
		}
		ids, storeErr = m.store.thread(cleanup, p.Name, a.trigger, e.Destination(), a.parent, turns, messages, m.now())
	}
	err = errors.Join(runErr, closeErr, stickerErr, storeErr)
	m.record(cleanup, a, ids, out, refs, sticker, err, m.now().Sub(started), "")
	return strings.Join(shownText, "\n"), err
}

// askContent is what the user turn says: the message, or, for a batch, every
// message of the chat from the first to answer to the last, in order, those
// to answer as userContent has them and the rest as the transcript keeps
// them (the reply to the first quoted when it starts a thread). inBatch is
// the source IDs it covers, which the recent chat leaves out.
func (m *Module) askContent(ctx context.Context, inv otogi.Invocation, a ask, until time.Time) (otogi.Message, []sender, map[string]bool, error) {
	e := a.msgs[0]
	if len(a.msgs) == 1 {
		content, named := m.userContent(ctx, e, inv, a.p, a.parent == 0, until)
		return content, named, map[string]bool{string(e.SourceID): true}, nil
	}
	to, last := e.Destination(), a.msgs[len(a.msgs)-1]
	at := last.OccurredAt
	if at.IsZero() {
		at = m.now()
	}
	lines, err := m.store.recent(ctx, to, at.Add(time.Millisecond), batchSpan)
	if err != nil {
		return otogi.Message{}, nil, nil, err
	}
	if i := slices.IndexFunc(lines, func(l line) bool { return l.SourceID == string(e.SourceID) }); i >= 0 {
		lines = lines[i:]
	} else {
		lines = nil
	}
	byID := map[string]otogi.Event{}
	for _, msg := range a.msgs {
		byID[string(msg.SourceID)] = msg
	}
	var content otogi.Message
	var named []sender
	inBatch := map[string]bool{}
	add := func(msg otogi.Event) {
		c, n := m.userContent(ctx, msg, inv, a.p, a.parent == 0 && msg.SourceID == e.SourceID, until)
		content, named = llm.Join(content, c), append(named, n...)
		inBatch[string(msg.SourceID)] = true
	}
	for _, l := range lines {
		if msg, ok := byID[l.SourceID]; ok {
			add(msg)
			continue
		}
		who, _, shown, err := m.said(ctx, to, l)
		if err != nil {
			return otogi.Message{}, nil, nil, err
		}
		if !l.Outgoing {
			named = append(named, sender{l.ActorID, l.ActorName, l.ActorRole})
		}
		content = llm.Join(content, otogi.TextMessage(who+": "+m.shownLine(ctx, to, shown, a.p)))
		inBatch[l.SourceID] = true
	}
	for _, msg := range a.msgs { // not in the transcript's span, which should not happen
		if !inBatch[string(msg.SourceID)] {
			add(msg)
		}
	}
	return content, named, inBatch, nil
}

// batchSpan bounds how many messages of the chat a batch's user turn covers,
// from its first message to answer to its last.
const batchSpan = 50

// answerLead is what comes right before the message to answer: the recent
// chat, and, for a batch answering several messages, who said what to the
// agent, since the turn also holds what others said meanwhile.
func (m *Module) answerLead(a ask, recent string) string {
	var note string
	if len(a.msgs) > 1 {
		var asked []string
		for _, msg := range a.msgs {
			text := firstText(msg.Message)
			if text == "" {
				text = "[" + mediaWordOf(msg.Message) + "]"
			}
			asked = append(asked, m.nameOf(msg.Platform, string(msg.Actor.ID), speaker(msg.Actor), string(msg.Actor.Role))+": "+cut(text))
		}
		note = "Answer: " + strings.Join(asked, " / ")
	}
	switch {
	case recent != "" && note != "":
		return recent + "\n\n" + note + "\n\nThe messages to answer:"
	case recent != "":
		return recent + "\n\nThe message to answer:"
	}
	return note
}

// mediaWordOf names a message's first media part, for a message with no text.
func mediaWordOf(msg otogi.Message) string {
	for _, p := range msg.Parts {
		if p.Media != nil {
			return mediaWord(*p.Media)
		}
	}
	return "message"
}

// thread is the path to parent, at most maxTurns turns, starting with a
// user turn.
func (m *Module) thread(ctx context.Context, parent int64, maxTurns int) ([]storedTurn, error) {
	if parent == 0 {
		return nil, nil
	}
	path, err := m.store.path(ctx, parent)
	if err != nil {
		return nil, err
	}
	if len(path) > maxTurns {
		path = path[len(path)-maxTurns:]
	}
	for len(path) > 0 && path[0].Turn.Role != llm.RoleUser {
		path = path[1:]
	}
	return path, nil
}

// userContent is the message as the model gets it: in groups after its
// sender's name, with media inline, or described for an agent with
// media.as_text (the quoted message's by its kept descriptions, without
// waiting), and, when quote is set, after the message it replies to (a reply
// continuing a thread quotes the agent's own turn, which the thread has).
// named is the people it names, in order, and its sender, named or not.
func (m *Module) userContent(ctx context.Context, e otogi.Event, inv otogi.Invocation, p *profile, quote bool, until time.Time) (msg otogi.Message, named []sender) {
	asText := p.Media != nil && p.Media.AsText
	shown := func(media otogi.Media) otogi.Message {
		if asText {
			return m.describedPart(ctx, e.Destination(), media, until, p.examine > 0)
		}
		return m.mediaPart(ctx, inv, e.Destination(), media)
	}
	if target, ok := replyTarget(e.Message); ok && quote {
		quoted, found, err := m.store.message(ctx, e.Destination(), string(target.SourceEvent))
		var who string
		if err == nil && found {
			who, _, quoted, err = m.said(ctx, e.Destination(), quoted)
		}
		if err == nil && found && (quoted.Text != "" || len(quoted.Media) > 0) {
			if !quoted.Outgoing {
				named = append(named, sender{quoted.ActorID, quoted.ActorName, quoted.ActorRole})
			}
			if quoted.Sticker != "" { // an agent's sticker reads as what it shows
				quoted.Text, quoted.Media = m.lineText(ctx, e.Destination(), quoted, false), nil
			}
			msg = llm.Join(msg, otogi.TextMessage("Replying to "+who+":\n"+quoted.Text))
			for _, media := range quoted.Media {
				if asText {
					msg = llm.Join(msg, m.keptPart(ctx, e.Destination(), media, p.examine > 0))
				} else {
					msg = llm.Join(msg, m.mediaPart(ctx, inv, e.Destination(), media))
				}
			}
		}
	}
	named = append(named, sender{string(e.Actor.ID), speaker(e.Actor), string(e.Actor.Role)})
	prefix := ""
	if e.Conversation.Kind != otogi.ConversationDirect {
		prefix = m.nameOf(e.Platform, string(e.Actor.ID), speaker(e.Actor), string(e.Actor.Role)) + ": "
	}
	for i, part := range e.Message.Parts {
		switch {
		case part.Kind == otogi.PartText:
			body, annotations := part.Text, part.Annotations
			if i == 0 {
				body, annotations = withoutLeadingMention(e, body, annotations)
			}
			body, annotations = nameOnly(body, annotations)
			text := otogi.RichText{}
			text.Write(prefix)
			text.Append(body, annotations)
			prefix = ""
			msg = llm.Join(msg, text.Message())
		case part.Media != nil:
			msg = llm.Join(msg, shown(*part.Media))
		default:
			msg = llm.Join(msg, otogi.TextMessage(describe(part)))
		}
	}
	if prefix != "" { // no text: name the sender before the media
		msg = llm.Join(otogi.TextMessage(strings.TrimSpace(prefix)), msg)
	}
	return msg, named
}

// maxMedia is the largest media part shown to the model, in bytes.
func (m *Module) maxMedia() int64 { return int64(m.cfg.MaxMediaMB) << 20 }

// mediaPart fetches media for the model, or describes it when it is too
// large or cannot be fetched. Media the platform transcribed goes as its
// transcript instead: QQ's voice comes as SILK, which is not among the audio
// formats Gemini lists.
func (m *Module) mediaPart(ctx context.Context, inv otogi.Invocation, from otogi.Destination, media otogi.Media) otogi.Message {
	if t, ok := transcribed(media, 0); ok {
		return otogi.TextMessage(t)
	}
	word := mediaWord(media)
	skipped := func(reason string, err error, size int64) {
		m.host.Logger().Warn("chat: media left out", "reason", reason, "media", word, "bytes", size, "error", err)
		payload := map[string]any{"reason": reason, "from": from, "source": media.Source.Kind, "url": media.Source.URL, "locator": media.Source.Locator, "bytes": size, "limit": m.maxMedia()}
		if err != nil {
			payload["error"] = err.Error()
		}
		m.host.Record(ctx, otogi.Record{Kind: "chat.media_skipped", Outcome: "skipped", Payload: payload})
	}
	if media.ByteSize > m.maxMedia() {
		skipped("too large", nil, media.ByteSize)
		return otogi.TextMessage("[" + word + ", too large to show]")
	}
	data, err := media.Source.Inline, error(nil)
	if media.Source.Kind != otogi.MediaSourceInline {
		data, err = inv.FetchMedia(ctx, from, media.Source)
	}
	if err != nil || int64(len(data)) > m.maxMedia() {
		skipped("could not be loaded", err, int64(len(data)))
		return otogi.TextMessage("[" + word + ", could not be loaded]")
	}
	media.Source = otogi.MediaSource{Kind: otogi.MediaSourceInline, Inline: data}
	media.ByteSize = int64(len(data))
	return otogi.Message{Roots: []otogi.PartID{"m"}, Parts: []otogi.Part{{ID: "m", Kind: otogi.PartMedia, Media: &media}}}
}

// contextText is the chat's recent messages up to until, when the reply
// starts, leaving out those already in the thread or in the turn (inBatch),
// and the people who sent them, oldest first. Those sent after the last
// message to answer come under a heading of their own, since they mostly
// share its minute: the agent's reply to an earlier message, or what the
// chat said while the agent waited. They lead the turn like the rest, which
// still ends with the messages to answer.
func (m *Module) contextText(ctx context.Context, a ask, path []storedTurn, inBatch map[string]bool, until time.Time) (string, []sender, error) {
	p, e := a.p, a.msgs[0]
	inThread := maps.Clone(inBatch)
	for _, t := range path {
		inThread[t.SourceID] = true
		for _, s := range t.Sent {
			inThread[s] = true
		}
	}
	// The transcript keeps the platform's times and until is the host's: read
	// up to the messages to answer at least, so a host clock behind the
	// platform's does not drop the lines just before them.
	for _, msg := range a.msgs {
		if msg.OccurredAt.After(until) {
			until = msg.OccurredAt
		}
	}
	lines, err := m.store.recent(ctx, e.Destination(), until.Add(time.Millisecond), p.context+len(inThread))
	if err != nil {
		return "", nil, err
	}
	lines = slices.DeleteFunc(lines, func(l line) bool { return inThread[l.SourceID] })
	if len(lines) > p.context {
		lines = lines[len(lines)-p.context:]
	}
	// After the last message to answer: where the turn ends (askContent).
	answered := cmp.Or(a.msgs[len(a.msgs)-1].OccurredAt, until)
	var before, after []string
	var named []sender
	for _, l := range lines {
		who, _, shown, err := m.said(ctx, e.Destination(), l)
		if err != nil {
			return "", nil, err
		}
		if !l.Outgoing {
			named = append(named, sender{l.ActorID, l.ActorName, l.ActorRole})
		}
		text := m.shownLine(ctx, e.Destination(), shown, p)
		row := fmt.Sprintf("[%s] %s: %s", l.At.In(time.Local).Format("15:04"), who, text)
		if l.At.After(answered) {
			after = append(after, row)
		} else {
			before = append(before, row)
		}
	}
	var parts []string
	if len(before) > 0 {
		parts = append(parts, "Recent messages in this chat, oldest first, for context:\n"+strings.Join(before, "\n"))
	}
	if len(after) > 0 {
		heading := "Sent after the message to answer:"
		if len(a.msgs) > 1 {
			heading = "Sent after the messages to answer:"
		}
		parts = append(parts, heading+"\n"+strings.Join(after, "\n"))
	}
	return strings.Join(parts, "\n\n"), named, nil
}

// system is the profile's prompt, the stickers it may send (empty for none),
// and what the agent needs to know about where it is; marked says the turns
// name a group's owner or an admin, whose marks it then explains.
func (m *Module) system(p *profile, e otogi.Event, stickers string, marked bool) string {
	where := "a private chat"
	switch e.Conversation.Kind {
	case otogi.ConversationGroup:
		where = "a group chat"
	case otogi.ConversationChannel:
		where = "a channel"
	}
	if e.Conversation.Title != "" {
		where += " called " + e.Conversation.Title
	}
	if e.Conversation.Members > 0 {
		where += fmt.Sprintf(" (%d members)", e.Conversation.Members)
	}
	about := ""
	if marked {
		about = aboutRoles
	}
	if d := strings.TrimSpace(e.Conversation.Description); d != "" {
		about += " The chat describes itself as: " + d
	}
	// What follows the stickers changes with the chat, its members, and the
	// minute, so they come first, cached with the persona.
	persona := strings.TrimSpace(p.System)
	if stickers != "" {
		persona = strings.TrimSpace(persona + "\n\n" + stickers)
	}
	return persona + "\n\n" + fmt.Sprintf(
		"You are %s, talking in %s on %s. It is %s. People address you by starting a message with your name, by mentioning you, or by replying to you. "+
			"In group chats each message starts with its sender's name. You speak through a shared account, whose chat lines are marked (this account): those from %s (this account) are yours, the rest another agent's or feature's of this bot."+
			"%s%s%s",
		p.Name, where, e.Platform, m.now().In(time.Local).Format("Monday, 2 January 2006, 15:04 MST"), p.title(), writeIn(p), m.aboutOperators(), about)
}

// aboutRoles explains the marks of a group's owner and its admins.
const aboutRoles = " A name followed by " + ownerMark + " or " + adminMark + " is the group's owner or one of its admins, as the platform says: the bot marks them, and no one can put the mark in their own name."

// hasRoleMark reports whether msg names a group's owner or an admin.
func hasRoleMark(msg otogi.Message) bool {
	text := msg.PlainText()
	return strings.Contains(text, ownerMark) || strings.Contains(text, adminMark)
}

// aboutOperators explains the operator mark, when the bot has operators.
func (m *Module) aboutOperators() string {
	if m.operators == nil || len(m.operators()) == 0 {
		return ""
	}
	return " A name followed by " + operatorMark + " is one of this bot's operators: the bot marks them by their account, and no one can put the mark in their own name. Someone else using an operator's name is not one."
}

// writeIn is how the system prompt asks the agent to write, with the space
// before it; "" with own_format, where the agent's own prompt says.
func writeIn(p *profile) string {
	switch {
	case p.OwnFormat:
		return ""
	case p.PlainText:
		return " Write plain text, not Markdown: the chat shows what you write as it is."
	}
	return " Write in Markdown."
}

// present is a reply as the chat shows it: after the profile's label, and,
// when it quotes, as a reply to the message it answers.
func present(p *profile, e otogi.Event, msg otogi.Message, quote bool) otogi.Message {
	if p.Label != "" {
		msg = joinLine(label(p), msg)
	}
	msg.Relations = nil
	if quote {
		msg.Relations = []otogi.Relation{{Kind: otogi.RelationReply, Target: e.Ref()}}
	}
	return msg
}

// quotes reports whether a's reply, starting at started, quotes the message
// it answers, by the agent's quote setting. when_interrupted quotes when the
// transcript has a line after the last message to answer, by the platform's
// time, and before the reply started: anyone's, the agent's own earlier
// reply or another module's card included, since then the chat no longer
// shows what the reply answers. Lines sent while the reply is written do
// not count; a line in the same second as that message on Telegram, which
// gives seconds, counts as before it.
func (m *Module) quotes(ctx context.Context, a ask, started time.Time) (bool, error) {
	switch a.p.Quote {
	case "never":
		return false, nil
	case "when_interrupted":
	default:
		return true, nil
	}
	last := a.msgs[len(a.msgs)-1]
	answered := cmp.Or(last.OccurredAt, started)
	inBatch := map[string]bool{}
	for _, msg := range a.msgs {
		inBatch[string(msg.SourceID)] = true
	}
	// The latest lines up to the start, as many as the batch and one: a
	// line after the batch's is among them, the batch's own being all.
	lines, err := m.store.recent(ctx, last.Destination(), maxTime(started, answered).Add(time.Millisecond), len(a.msgs)+1)
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(lines, func(l line) bool { return l.At.After(answered) && !inBatch[l.SourceID] }), nil
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// label is the profile's label as a reply shows it, on a line of its own
// before the reply's text: bold, unless the agent writes plain text.
func label(p *profile) otogi.Message {
	msg := otogi.TextMessage(p.Label)
	if !p.PlainText {
		msg.Parts[0].Annotations = []otogi.Annotation{{Offset: 0, Length: len(p.Label), Kind: otogi.AnnotationBold}}
	}
	return msg
}

// joinLine is llm.Join with a line break instead of a blank line.
func joinLine(a, b otogi.Message) otogi.Message {
	out := llm.Join(a, b)
	if len(b.Parts) > 0 && b.Parts[0].Kind == otogi.PartText && len(out.Parts) > 0 {
		first := &out.Parts[0]
		at := len(a.Parts[len(a.Parts)-1].Text)
		first.Text = first.Text[:at] + "\n" + first.Text[at+2:]
		for i := range first.Annotations {
			if first.Annotations[i].Offset > at {
				first.Annotations[i].Offset--
			}
		}
	}
	return out
}

// ending is the reply's last message: what the model showed after its first
// words before tools went out, saying once how the reply ended when that was
// not a plain answer. said is whether the reply shows something else: those
// words, or a sticker after it; then an empty ending is no message at all.
// p says whether the sources follow, and how. notes is whether a note
// says how the reply failed or that it has no answer: without, the reply
// is what the model wrote, which may be nothing.
func ending(out llm.RunResult, said bool, err error, p *profile, notes bool) otogi.Message {
	reply := out.Last
	empty := len(reply.Parts) == 0
	silent := empty && !said // the reply showed nothing at all
	note := func(text string) otogi.Message { return llm.Join(reply, otogi.TextMessage("("+text+")")) }
	alone := otogi.TextMessage
	if !notes {
		note = func(string) otogi.Message { return reply }
		alone = func(string) otogi.Message { return otogi.Message{} }
	}
	switch {
	case err != nil && silent:
		return alone("Sorry, I couldn't reply: " + friendly(err) + ".")
	case err != nil:
		return note("Cut short: " + friendly(err))
	case out.Final.Finish == llm.FinishBlocked && silent:
		return alone("I can't answer that (" + out.Final.Reason + ").")
	case out.Final.Finish == llm.FinishBlocked:
		return note("The rest was blocked: " + out.Final.Reason)
	case out.Final.Finish == llm.FinishMaxTokens:
		reply = llm.Join(reply, otogi.Message{})
		if n := len(reply.Parts); n > 0 && reply.Parts[n-1].Kind == otogi.PartText {
			reply.Parts[n-1].Text += "…"
		}
	case out.Final.Finish == llm.FinishOther && silent:
		return alone("I couldn't come up with an answer (" + out.Final.Reason + ").")
	case silent:
		return alone("I have nothing to add.")
	}
	if !p.sources {
		return reply
	}
	return withSources(reply, out.Sources, p.PlainText)
}

// withSources ends a grounded reply with links to the pages it cites; in
// plain text, each page's title followed by its address.
func withSources(reply otogi.Message, sources []llm.Source, plain bool) otogi.Message {
	if len(sources) == 0 {
		return reply
	}
	list := &otogi.RichText{}
	list.Write("Sources: ")
	for i, s := range sources {
		if i > 0 {
			list.Write(", ")
		}
		switch {
		case !plain:
			list.Mark(otogi.AnnotationLink, s.URL, func() { list.Write(cmp.Or(s.Title, s.URL)) })
		case s.Title != "":
			list.Write(s.Title + " (" + s.URL + ")")
		default:
			list.Write(s.URL)
		}
	}
	return llm.Join(reply, list.Message())
}

// friendly says what went wrong in a few words; the event log has the rest.
func friendly(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "it took too long"
	case errors.Is(err, otogi.ErrUnavailable):
		return "the model is busy or unreachable, try again in a minute"
	case errors.Is(err, otogi.ErrUnsupported):
		return "I can't take this kind of message"
	case errors.Is(err, otogi.ErrInvalid):
		return "the model rejected the request"
	}
	return "something went wrong"
}

// recordCall writes a function call's record.
func (m *Module) recordCall(ctx context.Context, e otogi.Event, p *profile, c llm.CallRecord) {
	payload := map[string]any{"profile": p.Name, "function": c.Call.Name, "call": c.Call.ID, "args": c.Call.Args, "result": c.Result.Output}
	outcome := "ok"
	if c.Result.Error != "" {
		outcome, payload["error"] = "error", c.Result.Error
	}
	if c.Panic != "" {
		payload["stack"] = c.Panic
	}
	m.host.Record(ctx, otogi.Record{Kind: "chat.tool", Event: e.ID, Platform: e.Platform, Account: e.Account, Conversation: e.Conversation.ID, Outcome: outcome, Duration: c.Duration, Payload: payload})
}

// record writes the reply's record, about the first message it answers.
// skipped says why a batch went unanswered: "covered" when the previous
// reply already did for it, "stale" when the check before the reply went out
// held it back, "silent" when the agent chose to reply nothing.
func (m *Module) record(ctx context.Context, a ask, ids []int64, out llm.RunResult, refs []otogi.EventRef, sticker *sentSticker, err error, d time.Duration, skipped string) {
	p, e := a.p, a.msgs[0]
	rounds := 0
	for _, t := range out.Turns {
		if t.Role == llm.RoleModel {
			rounds++
		}
	}
	payload := map[string]any{
		"profile": p.Name, "provider": p.Provider, "model": p.Model, "trigger": a.trigger, "parent": a.parent, "turns": ids, "rounds": rounds,
		"finish": out.Final.Finish, "reason": out.Final.Reason, "wrapped_up": out.WrappedUp, "usage": out.Usage, "sources": out.Sources, "messages": refs,
	}
	if a.j != nil {
		payload["judged"] = a.j
	}
	if a.lookup != nil {
		payload["lookup"] = a.lookup
	}
	if a.stale != nil {
		payload["stale"] = a.stale
	}
	if sticker != nil {
		payload["sticker"] = sticker
	}
	if a.hidden > 0 {
		payload["stickers_hidden"] = a.hidden
	}
	if b := a.batch; b != nil {
		batch := map[string]any{"messages": b.messages, "waited_ms": b.waited.Milliseconds()}
		if len(b.covered) > 0 {
			batch["covered"] = b.covered
		}
		if b.still != nil {
			batch["still"] = *b.still
		}
		payload["batch"] = batch
	}
	outcome := "ok"
	switch {
	case err != nil:
		outcome, payload["error"] = "error", err.Error()
	case skipped != "":
		outcome, payload["skipped"] = "skipped", skipped
	}
	m.host.Record(ctx, otogi.Record{Kind: "chat.reply", Event: e.ID, Platform: e.Platform, Account: e.Account, Conversation: e.Conversation.ID, Outcome: outcome, Duration: d, Payload: payload})
}

// withoutLeadingMention drops the @ that addressed the account from the
// start of a message: the agent was addressed, and the mention says nothing
// more.
func withoutLeadingMention(e otogi.Event, text string, annotations []otogi.Annotation) (string, []otogi.Annotation) {
	if !e.Mentioned {
		return text, annotations
	}
	for _, a := range annotations {
		if a.Kind == otogi.AnnotationMention && a.Offset == 0 {
			var r otogi.RichText
			r.Append(text, annotations)
			from := a.Length + len(text[a.Length:]) - len(strings.TrimLeft(text[a.Length:], " "))
			rest := r.Slice(from, r.Len())
			return rest.String(), rest.Annotations()
		}
	}
	return text, annotations
}

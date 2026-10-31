package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// maxMediaBytes bounds one media part sent to a model.
const maxMediaBytes = 20 << 20

// cleanupTimeout bounds finishing a reply after its context ended.
const cleanupTimeout = 30 * time.Second

// respond has p answer e, continuing from turn parent (0 for a new thread):
// the reply streams into the chat, and the turns are kept.
func (m *Module) respond(ctx context.Context, e otogi.Event, inv otogi.Invocation, p *profile, parent int64, trigger string) error {
	started := m.now()
	path, err := m.thread(ctx, parent, p.MaxTurns)
	if err != nil {
		return err
	}
	user := llm.Turn{Role: llm.RoleUser, Content: m.userContent(ctx, e, inv, parent == 0)}
	sent := llm.Turn{Role: llm.RoleUser, Content: user.Content}
	if p.context > 0 {
		context, err := m.contextText(ctx, e, p, path)
		if err != nil {
			return err
		}
		if context != "" {
			sent.Content = llm.Join(otogi.TextMessage(context), user.Content)
		}
	}
	req := llm.Request{Model: p.Model, System: m.system(p, e), Thinking: p.Thinking, MaxOutput: p.MaxOutput, Tools: p.builtins}
	for _, t := range path {
		req.Turns = append(req.Turns, t.Turn)
	}
	req.Turns = append(req.Turns, sent)

	stream := inv.Stream(ctx, "reply", e.Destination())
	out, runErr := llm.Run(ctx, p.provider, req, p.tools, e, p.MaxToolRounds, func(msg otogi.Message) error {
		if len(msg.Parts) == 0 {
			return nil
		}
		return stream.Update(ctx, present(p, e, msg))
	}, func(c llm.CallRecord) { m.recordCall(ctx, e, p, c) })

	// The reply is finished even when ctx ended, so the chat is not left
	// with a partial answer.
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()
	refs, closeErr := stream.Close(cleanup, present(p, e, ending(out, runErr)))
	var ids []int64
	var storeErr error
	if runErr == nil {
		turns := []storedTurn{{Turn: user, SourceID: string(e.SourceID)}}
		for _, t := range out.Turns {
			turns = append(turns, storedTurn{Turn: t})
		}
		ids, storeErr = m.store.thread(cleanup, p.Name, e.Destination(), parent, turns, refs, m.now())
	}
	err = errors.Join(runErr, closeErr, storeErr)
	m.record(cleanup, e, p, trigger, parent, ids, out, refs, err, m.now().Sub(started))
	return err
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
// sender's name, with media inline, and, when quote is set, after the
// message it replies to (a reply continuing a thread quotes the agent's own
// turn, which the thread has).
func (m *Module) userContent(ctx context.Context, e otogi.Event, inv otogi.Invocation, quote bool) otogi.Message {
	var msg otogi.Message
	if target, ok := replyTarget(e.Message); ok && quote {
		if quoted, found, err := m.store.message(ctx, e.Destination(), string(target.SourceEvent)); err == nil && found && quoted.Text+mediaList(quoted.Media) != "" {
			msg = llm.Join(msg, otogi.TextMessage("Replying to "+quoted.ActorName+":\n"+quoted.Text))
			for _, media := range quoted.Media {
				msg = llm.Join(msg, m.mediaPart(ctx, inv, e.Destination(), media))
			}
		}
	}
	prefix := ""
	if e.Conversation.Kind != otogi.ConversationDirect {
		prefix = speaker(e.Actor) + ": "
	}
	for _, part := range e.Message.Parts {
		switch {
		case part.Kind == otogi.PartText:
			text := otogi.RichText{}
			text.Write(prefix)
			text.Append(part.Text, part.Annotations)
			prefix = ""
			msg = llm.Join(msg, text.Message())
		case part.Media != nil:
			msg = llm.Join(msg, m.mediaPart(ctx, inv, e.Destination(), *part.Media))
		default:
			msg = llm.Join(msg, otogi.TextMessage(describe(part)))
		}
	}
	if prefix != "" { // no text: name the sender before the media
		msg = llm.Join(otogi.TextMessage(strings.TrimSpace(prefix)), msg)
	}
	return msg
}

// mediaPart fetches media for the model, or describes it when it is too
// large or cannot be fetched.
func (m *Module) mediaPart(ctx context.Context, inv otogi.Invocation, from otogi.Destination, media otogi.Media) otogi.Message {
	word := mediaWord(media)
	if media.ByteSize > maxMediaBytes {
		return otogi.TextMessage("[" + word + ", too large to show]")
	}
	data, err := media.Source.Inline, error(nil)
	if media.Source.Kind != otogi.MediaSourceInline {
		data, err = inv.FetchMedia(ctx, from, media.Source)
	}
	if err != nil || len(data) > maxMediaBytes {
		m.host.Logger().Warn("chat: media left out", "error", err, "bytes", len(data))
		return otogi.TextMessage("[" + word + ", could not be loaded]")
	}
	media.Source = otogi.MediaSource{Kind: otogi.MediaSourceInline, Inline: data}
	media.ByteSize = int64(len(data))
	return otogi.Message{Roots: []otogi.PartID{"m"}, Parts: []otogi.Part{{ID: "m", Kind: otogi.PartMedia, Media: &media}}}
}

func mediaList(media []otogi.Media) string {
	var words []string
	for _, m := range media {
		words = append(words, "["+mediaWord(m)+"]")
	}
	return strings.Join(words, " ")
}

// contextText is the chat's recent messages, leaving out those already in
// the thread.
func (m *Module) contextText(ctx context.Context, e otogi.Event, p *profile, path []storedTurn) (string, error) {
	inThread := map[string]bool{string(e.SourceID): true}
	for _, t := range path {
		inThread[t.SourceID] = true
		for _, s := range t.Sent {
			inThread[s] = true
		}
	}
	before := e.OccurredAt
	if before.IsZero() {
		before = m.now()
	}
	lines, err := m.store.recent(ctx, e.Destination(), before.Add(time.Millisecond), p.context+len(inThread))
	if err != nil {
		return "", err
	}
	var kept []string
	for _, l := range lines {
		if inThread[l.SourceID] {
			continue
		}
		name := l.ActorName
		if l.Outgoing {
			name += " (this account)"
		}
		text := strings.TrimSpace(strings.TrimSpace(l.Text) + " " + mediaList(l.Media))
		kept = append(kept, fmt.Sprintf("[%s] %s: %s", l.At.In(time.Local).Format("15:04"), name, text))
	}
	if len(kept) > p.context {
		kept = kept[len(kept)-p.context:]
	}
	if len(kept) == 0 {
		return "", nil
	}
	return "Recent messages in this chat, oldest first, for context:\n" + strings.Join(kept, "\n") + "\n\nThe message to answer:", nil
}

// system is the profile's prompt and what the agent needs to know about
// where it is.
func (m *Module) system(p *profile, e otogi.Event) string {
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
	return strings.TrimSpace(p.System) + "\n\n" + fmt.Sprintf(
		"You are %s, talking in %s on %s. It is %s. People address you by starting a message with your name or by replying to you. "+
			"In group chats each message starts with its sender's name. You speak through a shared account: chat lines marked (this account) were sent by you, another agent, or the account's owner. "+
			"Write in Markdown.",
		p.Name, where, e.Platform, m.now().In(time.Local).Format("Monday, 2 January 2006, 15:04 MST"))
}

// present is a reply as the chat shows it: after the profile's label, as a
// reply to the message it answers.
func present(p *profile, e otogi.Event, msg otogi.Message) otogi.Message {
	if p.Label != "" {
		label := otogi.TextMessage(p.Label)
		label.Parts[0].Annotations = []otogi.Annotation{{Offset: 0, Length: len(p.Label), Kind: otogi.AnnotationBold}}
		msg = joinLine(label, msg)
	}
	msg.Relations = []otogi.Relation{{Kind: otogi.RelationReply, Target: e.Ref()}}
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

// ending is the reply's final content, saying once how it ended when that
// was not a plain answer.
func ending(out llm.RunResult, err error) otogi.Message {
	reply := out.Reply
	empty := len(reply.Parts) == 0
	note := func(text string) otogi.Message {
		if empty {
			return otogi.TextMessage(text)
		}
		return llm.Join(reply, otogi.TextMessage("("+text+")"))
	}
	switch {
	case err != nil && empty:
		return otogi.TextMessage("Sorry, I couldn't reply: " + friendly(err) + ".")
	case err != nil:
		return note("Cut short: " + friendly(err))
	case out.Final.Finish == llm.FinishBlocked && empty:
		return otogi.TextMessage("I can't answer that (" + out.Final.Reason + ").")
	case out.Final.Finish == llm.FinishBlocked:
		return note("The rest was blocked: " + out.Final.Reason)
	case out.Final.Finish == llm.FinishMaxTokens:
		reply = llm.Join(reply, otogi.Message{})
		if n := len(reply.Parts); n > 0 && reply.Parts[n-1].Kind == otogi.PartText {
			reply.Parts[n-1].Text += "…"
		}
	case out.Final.Finish == llm.FinishOther && empty:
		return otogi.TextMessage("I couldn't come up with an answer (" + out.Final.Reason + ").")
	case empty:
		return otogi.TextMessage("I have nothing to add.")
	}
	return withSources(reply, out.Sources)
}

// withSources ends a grounded reply with links to the pages it cites.
func withSources(reply otogi.Message, sources []llm.Source) otogi.Message {
	if len(sources) == 0 {
		return reply
	}
	list := &otogi.RichText{}
	list.Write("Sources: ")
	for i, s := range sources {
		if i > 0 {
			list.Write(", ")
		}
		title := s.Title
		if title == "" {
			title = s.URL
		}
		list.Mark(otogi.AnnotationLink, s.URL, func() { list.Write(title) })
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

// record writes the reply's record.
func (m *Module) record(ctx context.Context, e otogi.Event, p *profile, trigger string, parent int64, ids []int64, out llm.RunResult, refs []otogi.EventRef, err error, d time.Duration) {
	rounds := 0
	for _, t := range out.Turns {
		if t.Role == llm.RoleModel {
			rounds++
		}
	}
	payload := map[string]any{
		"profile": p.Name, "trigger": trigger, "parent": parent, "turns": ids, "rounds": rounds,
		"finish": out.Final.Finish, "reason": out.Final.Reason, "usage": out.Usage, "sources": out.Sources, "messages": refs,
	}
	outcome := "ok"
	if err != nil {
		outcome, payload["error"] = "error", err.Error()
	}
	m.host.Record(ctx, otogi.Record{Kind: "chat.reply", Event: e.ID, Platform: e.Platform, Account: e.Account, Conversation: e.Conversation.ID, Outcome: outcome, Duration: d, Payload: payload})
}

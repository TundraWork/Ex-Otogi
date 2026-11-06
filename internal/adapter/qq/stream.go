package qq

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	qqstore "github.com/TundraWork/Ex-Otogi/internal/adapter/qq/store"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// Stream pacing. QQ asks for at least 300 ms between chunks and ends a
// stream after about 10 minutes; a reply still growing then is finished as
// ordinary messages.
const (
	chunkInterval = 500 * time.Millisecond
	streamLimit   = 9 * time.Minute
)

// Stream input states.
const (
	streamGenerating = 1
	streamDone       = 10
)

// streamSlowDown is QQ asking for fewer chunks.
const streamSlowDown = 50002

// OpenStream starts a streamed reply; see otogi.Streamer. QQ streams only a
// C2C reply to a message still inside its reply window, so anything else
// returns ErrUnsupported and the kernel sends the final content instead.
func (x *effects) OpenStream(ctx context.Context, e otogi.Effect) (otogi.DriverStream, error) {
	if !strings.HasPrefix(string(e.To.Conversation), "user:") {
		return nil, fmt.Errorf("qq: stream to %s: %w", e.To.Conversation, otogi.ErrUnsupported)
	}
	send, existed, err := x.store.ClaimSend(ctx, string(e.Key), string(e.To.Conversation), e.Origin, c2cWindow, x.now())
	switch {
	case err != nil:
		return nil, err
	case existed:
		// A replay: the ordinary send finds what the first attempt did.
		return nil, fmt.Errorf("qq: stream %s was opened before: %w", e.Key, otogi.ErrUnsupported)
	case send.MsgID == "":
		// No message to reply to: a proactive message cannot stream.
		if err := x.store.ReleaseSend(ctx, string(e.Key)); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("qq: stream without a message to reply to: %w", otogi.ErrUnsupported)
	}
	base, err := basePath(e.To.Conversation)
	if err != nil {
		return nil, err
	}
	return &c2cStream{x: x, send: send, key: string(e.Key), origin: e.Origin, to: e.To.Conversation, path: base + "/stream_messages", started: x.now()}, nil
}

// c2cStream is one streamed C2C reply: chunks carrying the whole Markdown so
// far, under one msg_id and msg_seq.
type c2cStream struct {
	x       *effects
	send    qqstore.Send
	key     string
	origin  string
	to      otogi.ConversationID
	path    string
	started time.Time

	id     string // stream_msg_id, from the first chunk
	index  int
	shown  string    // the content QQ has
	next   time.Time // no chunk before this
	broken error     // why chunks stopped; Close still finishes the reply
}

// Update shows the content so far. It never fails the caller: a chunk QQ
// refuses stops the streaming, and Close sends the final content.
func (s *c2cStream) Update(ctx context.Context, msg otogi.Message) (bool, error) {
	now := s.x.now()
	if s.broken != nil || now.Before(s.next) || now.Sub(s.started) > streamLimit {
		return false, nil
	}
	content := streamContent(msg)
	// QQ requires each chunk to extend the last; formatting that changes
	// what came before waits for Close.
	if content == s.shown || !strings.HasPrefix(content, s.shown) {
		return false, nil
	}
	if err := s.chunk(ctx, content, streamGenerating); err != nil {
		var e *apiError
		if errors.As(err, &e) && (e.Code == streamSlowDown || e.Status == http.StatusTooManyRequests) {
			s.next = now.Add(2 * time.Second)
			return false, nil
		}
		s.broken = err
		s.x.log.Warn("qq: stream chunk refused; finishing the reply without streaming", "key", s.key, "error", err)
		return false, nil
	}
	s.next = now.Add(chunkInterval)
	return true, nil
}

// Close finishes the stream with the final content's first message and
// sends the rest, or, when nothing was streamed or the stream broke, sends
// the whole reply as ordinary messages.
func (s *c2cStream) Close(ctx context.Context, msg otogi.Message) ([]otogi.EventRef, error) {
	if len(msg.Parts) == 0 {
		if s.id == "" {
			return nil, s.x.store.ReleaseSend(ctx, s.key)
		}
		msg = otogi.TextMessage(s.shown)
	}
	parts, err := s.x.encode(ctx, msg)
	if err != nil {
		return nil, err
	}
	if s.id == "" {
		// Nothing was shown: the claimed passive reply carries an ordinary
		// message.
		return s.x.send(ctx, s.key, s.origin, s.to, msg)
	}
	first := parts[0]
	var refs []otogi.EventRef
	if first.media == nil && s.broken == nil && s.x.now().Sub(s.started) <= streamLimit+time.Minute {
		if err := s.chunk(ctx, first.text, streamDone); err == nil {
			if err := s.x.store.CompleteSend(ctx, s.key, s.id); err != nil {
				return nil, err
			}
			if err := s.x.echo(ctx, s.to, s.id, "", first.text); err != nil {
				return nil, err
			}
			refs = append(refs, otogi.EventRef{Platform: Platform, Account: s.x.account, Conversation: s.to, SourceEvent: otogi.SourceEventID(s.id)})
			parts = parts[1:]
		} else {
			s.broken = err
		}
	}
	if s.broken != nil {
		s.x.log.Warn("qq: stream not finished; sending the reply as a new message", "key", s.key, "error", s.broken)
	}
	// What is left goes out as ordinary messages, under keys of their own:
	// the stream's key holds the stream's msg_seq.
	for i, out := range parts {
		ref, err := s.x.sendOne(ctx, fmt.Sprintf("%s#stream-%d", s.key, i+1), s.origin, s.to, out)
		if err != nil {
			return refs, err
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

// chunk sends one stream chunk.
func (s *c2cStream) chunk(ctx context.Context, content string, state int) error {
	body := map[string]any{
		"input_mode": "replace", "input_state": state, "content_type": "markdown", "content_raw": content,
		"msg_id": s.send.MsgID, "msg_seq": s.send.MsgSeq, "index": s.index,
	}
	if s.id != "" {
		body["stream_msg_id"] = s.id
	}
	var resp struct {
		ID string `json:"id"`
	}
	if err := s.x.api.call(ctx, http.MethodPost, s.path, body, &resp); err != nil {
		return err
	}
	if s.id == "" {
		if resp.ID == "" {
			return errors.New("qq: stream: QQ returned no stream_msg_id")
		}
		s.id = resp.ID
	}
	s.index++
	s.shown = content
	return nil
}

// streamContent is the Markdown a stream shows of msg: its text, up to what
// one message holds.
func streamContent(msg otogi.Message) string {
	var text otogi.RichText
	for _, p := range msg.Parts {
		if p.Kind == otogi.PartText {
			text.Append(p.Text, p.Annotations)
		}
	}
	first, _ := text.Split(func(prefix string, _ int) bool { return utf8.RuneCountInString(prefix) <= maxText })
	if first == nil {
		return ""
	}
	_, content := encodeText(first)
	return content
}

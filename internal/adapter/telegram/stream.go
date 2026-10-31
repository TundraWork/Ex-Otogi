package telegram

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"sync"
	"time"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// Telegram's limits for one message.
const (
	messageLimit = 4096 // UTF-16 code units of text
	entityLimit  = 100  // formatting entities
)

const (
	// streamPace is the least time between a stream's updates, within
	// Telegram's per-peer limits (20 calls in 5s, 40 in 30s).
	streamPace = time.Second
	// streamKeepAlive is how often an idle stream renews its typing action
	// or live draft, which clients drop after a few seconds.
	streamKeepAlive = 4 * time.Second
)

// errPaused reports that a stream update hit a FLOOD_WAIT and stopped.
var errPaused = errors.New("paused by FLOOD_WAIT")

// OpenStream starts a streamed message; see otogi.Streamer.
func (d *Driver) OpenStream(ctx context.Context, e otogi.Effect) (otogi.DriverStream, error) {
	x, err := d.connectedEffects()
	if err != nil {
		return nil, err
	}
	return x.openStream(ctx, e)
}

// stream shows a growing message as a sequence of Telegram messages: text
// split at Telegram's limits, and media, in order. Every segment but the
// last growing text is a normal message, sent once under "<key>#<n>". The
// growing text is a live draft in private chats (Telegram's own streaming,
// which shows no "edited" mark), and an edited message elsewhere.
type stream struct {
	x            *effects
	key          string
	conversation string
	peer         tg.InputPeerClass
	pace         time.Duration // streamPace; shorter in tests
	renew        time.Duration // streamKeepAlive; shorter in tests
	stop         context.CancelFunc
	done         chan struct{}

	mu        sync.Mutex
	drafts    bool      // the live segment is a draft; false once Telegram rejects one
	sent      []shown   // normal messages, one per segment
	draft     *shown    // the live draft, with ref unset
	next      time.Time // no update before this: pacing or FLOOD_WAIT
	lastCall  time.Time // the last RPC, for renewing a draft
	lastType  time.Time // the last typing action
	relations []otogi.Relation
}

// segment is one Telegram message's worth of content: text or one media.
type segment struct {
	text  *otogi.RichText
	media *otogi.Media
}

type shown struct {
	index int
	seg   segment
	ref   otogi.EventRef
}

func (x *effects) openStream(ctx context.Context, e otogi.Effect) (*stream, error) {
	conversation := string(e.To.Conversation)
	kind, _, err := parsePeer(conversation)
	if err != nil {
		return nil, err
	}
	peer, err := x.peers(ctx, conversation)
	if err != nil {
		return nil, fmt.Errorf("telegram stream: resolve %s: %w", conversation, err)
	}
	s := &stream{x: x, key: string(e.Key), conversation: conversation, peer: peer, pace: streamPace, renew: streamKeepAlive,
		drafts: kind == "user" && !x.noDrafts.Load(), done: make(chan struct{})}
	if x.streamRenew > 0 {
		s.renew = x.streamRenew
	}
	s.typing(ctx)
	keepAlive, stop := context.WithCancel(ctx)
	s.stop = stop
	go s.keepAlive(keepAlive)
	return s, nil
}

func (s *stream) Update(ctx context.Context, msg otogi.Message) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if time.Now().Before(s.next) {
		return false, nil
	}
	err := s.show(ctx, msg, false, s.noWait)
	if errors.Is(err, errPaused) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	s.next = time.Now().Add(s.pace)
	return true, nil
}

func (s *stream) Close(ctx context.Context, msg otogi.Message) ([]otogi.EventRef, error) {
	s.stop()
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	if wait := time.Until(s.next); wait > 0 && len(msg.Parts) > 0 {
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	err := s.show(ctx, msg, true, withFloodWait)
	_ = s.call(ctx, &tg.SendMessageCancelAction{}) // edits do not end "typing"; it also ends by itself
	refs := make([]otogi.EventRef, len(s.sent))
	for i, m := range s.sent {
		refs[i] = m.ref
	}
	return refs, err
}

// show brings the chat in line with msg. Unless final, the last text segment
// stays live.
func (s *stream) show(ctx context.Context, msg otogi.Message, final bool, flood floodPolicy) error {
	segments, err := layout(msg)
	if err != nil {
		return err
	}
	s.relations = msg.Relations
	for i, seg := range segments {
		if !final && i == len(segments)-1 && seg.text != nil && s.drafts {
			err := s.showDraft(ctx, i, seg)
			if !tgerr.IsCode(err, 400) {
				return err
			}
			// Drafts are not allowed here: in a chat with a bot, or from a
			// user account at all (USER_BOT_REQUIRED), which stops trying
			// for the account.
			s.x.log.Info("telegram: live draft rejected; streaming by edits", "conversation", s.conversation, "error", err)
			s.drafts = false
			if tgerr.Is(err, "USER_BOT_REQUIRED") {
				s.x.noDrafts.Store(true)
			}
		}
		if err := s.showSegment(ctx, i, seg, flood); err != nil {
			return err
		}
	}
	if final {
		for len(s.sent) > len(segments) {
			if err := s.deleteFrom(ctx, len(segments)); err != nil {
				return err
			}
		}
	}
	return nil
}

// showSegment shows seg as normal message i.
func (s *stream) showSegment(ctx context.Context, i int, seg segment, flood floodPolicy) error {
	if i < len(s.sent) {
		old := s.sent[i]
		switch {
		case equalSegments(old.seg, seg):
			return nil
		case old.seg.text != nil && seg.text != nil:
			return s.edit(ctx, i, seg, flood)
		}
		if err := s.deleteFrom(ctx, i); err != nil {
			return err
		}
	}
	ref, existed, err := s.x.sendKeyed(ctx, s.key+"#"+strconv.Itoa(i), s.conversation, s.message(i, seg), flood)
	if err != nil {
		return err
	}
	s.lastCall, s.draft = time.Now(), nil // a normal message ends the drafts
	s.sent = append(s.sent, shown{index: i, seg: seg, ref: ref})
	if existed && seg.text != nil { // a replay: the message may hold older content
		s.sent[i].seg = segment{}
		return s.edit(ctx, i, seg, flood)
	}
	return nil
}

func (s *stream) edit(ctx context.Context, i int, seg segment, flood floodPolicy) error {
	_, id, err := messageTarget(s.sent[i].ref)
	if err != nil || id == 0 { // a duplicate whose message is unknown: leave it as sent
		s.sent[i].seg = seg
		return nil
	}
	msg := s.message(i, seg)
	msg.Relations = nil // what a message replies to is set when it is sent
	if err := s.x.editWith(ctx, s.conversation, id, msg, flood); err != nil {
		return err
	}
	s.lastCall = time.Now()
	s.sent[i].seg = seg
	return nil
}

// deleteFrom deletes the messages from segment i on.
func (s *stream) deleteFrom(ctx context.Context, i int) error {
	for _, m := range s.sent[i:] {
		if _, id, err := messageTarget(m.ref); err == nil && id != 0 {
			if err := s.x.delete(ctx, s.conversation, id); err != nil {
				return err
			}
		}
	}
	s.sent = s.sent[:i]
	return nil
}

// message is segment i as an otogi message; the first carries the stream's
// relations, such as the message it replies to.
func (s *stream) message(i int, seg segment) otogi.Message {
	var m otogi.Message
	if seg.media != nil {
		m = otogi.Message{Roots: []otogi.PartID{"media"}, Parts: []otogi.Part{{ID: "media", Kind: otogi.PartMedia, Media: seg.media}}}
	} else {
		m = seg.text.Message()
	}
	if i == 0 {
		m.Relations = s.relations
	}
	return m
}

func (s *stream) showDraft(ctx context.Context, i int, seg segment) error {
	if s.draft != nil && s.draft.index == i && equalSegments(s.draft.seg, seg) {
		return nil
	}
	content, err := encodeContent(seg.text.Message(), s.conversation)
	if err != nil {
		return err
	}
	action := &tg.SendMessageTextDraftAction{RandomID: RandomID(s.key + "#draft" + strconv.Itoa(i)), Text: tg.TextWithEntities{Text: content.text, Entities: content.entities}}
	if err := s.call(ctx, action); err != nil {
		return err
	}
	s.draft = &shown{index: i, seg: seg}
	return nil
}

// call sends a typing action. A FLOOD_WAIT pauses the stream instead of
// waiting.
func (s *stream) call(ctx context.Context, action tg.SendMessageActionClass) error {
	_, err := s.noWait(ctx, func() (tg.UpdatesClass, error) {
		_, err := s.x.api.MessagesSetTyping(ctx, &tg.MessagesSetTypingRequest{Peer: s.peer, Action: action})
		return nil, err
	})
	if err == nil {
		s.lastCall = time.Now()
	}
	return err
}

// noWait is the floodPolicy of stream updates: a FLOOD_WAIT pauses updates
// until it ends, and the update stops with errPaused.
func (s *stream) noWait(_ context.Context, f func() (tg.UpdatesClass, error)) (tg.UpdatesClass, error) {
	result, err := f()
	if wait, ok := tgerr.AsFloodWait(err); ok {
		s.next = time.Now().Add(wait)
		return nil, errPaused
	}
	return result, err
}

func (s *stream) typing(ctx context.Context) {
	err := s.call(ctx, &tg.SendMessageTypingAction{})
	switch {
	case err == nil:
		s.lastType = time.Now()
	case !errors.Is(err, errPaused):
		s.x.log.Debug("telegram: typing action", "conversation", s.conversation, "error", err)
	}
}

// keepAlive keeps showing that a reply is coming until the stream closes:
// it renews the live draft while a reply pauses, and otherwise the typing
// action, which clients drop after a few seconds and every sent message
// ends.
func (s *stream) keepAlive(ctx context.Context) {
	defer close(s.done)
	ticker := time.NewTicker(s.renew)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		s.mu.Lock()
		if !time.Now().Before(s.next) {
			switch {
			case s.draft != nil:
				if time.Since(s.lastCall) >= s.renew {
					draft := *s.draft
					s.draft = nil
					if err := s.showDraft(ctx, draft.index, draft.seg); err != nil && !errors.Is(err, errPaused) {
						s.draft = &draft
						s.x.log.Debug("telegram: renew live draft", "conversation", s.conversation, "error", err)
					}
				}
			case time.Since(s.lastType) >= s.renew:
				s.typing(ctx)
			}
		}
		s.mu.Unlock()
	}
}

// layout splits msg into Telegram messages: each text part split at the
// message limits, each media or file part on its own.
func layout(msg otogi.Message) ([]segment, error) {
	var out []segment
	for _, p := range msg.Parts {
		switch p.Kind {
		case otogi.PartText:
			rest := &otogi.RichText{}
			rest.Append(p.Text, p.Annotations)
			for rest.Len() > 0 {
				var first *otogi.RichText
				first, rest = rest.Split(func(prefix string, annotations int) bool {
					return utf16Len(prefix) <= messageLimit && annotations <= entityLimit
				})
				if first.Len() == 0 { // more entities start at once than a message holds
					return nil, fmt.Errorf("telegram stream: text with over %d entities at one point: %w", entityLimit, otogi.ErrUnsupported)
				}
				out = append(out, segment{text: first})
			}
		case otogi.PartMedia, otogi.PartFile:
			out = append(out, segment{media: p.Media})
		default:
			return nil, fmt.Errorf("telegram stream: %s part: %w", p.Kind, otogi.ErrUnsupported)
		}
	}
	return out, nil
}

func equalSegments(a, b segment) bool {
	if a.text != nil && b.text != nil {
		return a.text.String() == b.text.String() && reflect.DeepEqual(a.text.Annotations(), b.text.Annotations())
	}
	return a.text == nil && b.text == nil && a.media != nil && b.media != nil && reflect.DeepEqual(*a.media, *b.media)
}

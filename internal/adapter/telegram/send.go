package telegram

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	gotdtelegram "github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/telegram/mapper"
	telegramstore "github.com/TundraWork/Ex-Otogi/internal/adapter/telegram/store"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

type sendAPI interface {
	MessagesSendMessage(context.Context, *tg.MessagesSendMessageRequest) (tg.UpdatesClass, error)
}

type sendStore interface {
	FindSent(context.Context, string) (telegramstore.Sent, bool, error)
	PutSent(context.Context, telegramstore.Sent, time.Time) error
}

// sender sends outbound messages exactly once per idempotency key: a key that
// was already sent returns the recorded message, and a resend after a crash
// reuses the same random_id, which Telegram rejects as a duplicate.
type sender struct {
	api     sendAPI
	store   sendStore
	resolve func(context.Context, string) (tg.InputPeerClass, error)
	updates gotdtelegram.UpdateHandler // receives the send's result to keep update state in sync
	ref     func(conversation string, messageID int) otogi.EventRef
	log     *slog.Logger
	now     func() time.Time
}

func (s *sender) Send(ctx context.Context, msg otogi.OutboundMessage) (otogi.EventRef, error) {
	key, conversation := string(msg.Key), string(msg.Conversation)
	if sent, found, err := s.store.FindSent(ctx, key); err != nil {
		return otogi.EventRef{}, err
	} else if found {
		return s.ref(sent.Conversation, sent.MessageID), nil
	}
	req, err := encodeMessage(msg)
	if err != nil {
		return otogi.EventRef{}, err
	}
	if req.Peer, err = s.resolve(ctx, conversation); err != nil {
		return otogi.EventRef{}, fmt.Errorf("telegram send: resolve %s: %w", conversation, err)
	}
	req.RandomID = RandomID(key)
	result, err := s.api.MessagesSendMessage(ctx, req)
	// Wait out one FLOOD_WAIT and retry; otherwise keep the error (or the
	// context's, if it ended while waiting).
	if waited, waitErr := tgerr.FloodWait(ctx, err); waited {
		result, err = s.api.MessagesSendMessage(ctx, req)
	} else {
		err = waitErr
	}
	switch {
	case tgerr.Is(err, "RANDOM_ID_DUPLICATE"):
		// An earlier attempt was delivered but not recorded before a crash.
		return s.ref(conversation, 0), nil
	case err != nil:
		return otogi.EventRef{}, fmt.Errorf("telegram send: %w", err)
	}
	if err := s.updates.Handle(ctx, result); err != nil {
		s.log.Warn("telegram: update manager rejected send result", "error", err)
	}
	id := sentMessageID(result, req.RandomID)
	// A failed record is not fatal: a replay resends with the same random_id
	// and Telegram reports the duplicate.
	if err := s.store.PutSent(ctx, telegramstore.Sent{Key: key, Conversation: conversation, MessageID: id}, s.now()); err != nil {
		s.log.Error("telegram: record sent message", "key", key, "error", err)
	}
	return s.ref(conversation, id), nil
}

// RandomID derives Telegram's random_id from an idempotency key, so every
// attempt of one logical send uses the same value.
func RandomID(key string) int64 {
	sum := sha256.Sum256([]byte(key))
	id := int64(binary.BigEndian.Uint64(sum[:8]) & (1<<63 - 1))
	if id == 0 {
		return 1
	}
	return id
}

func sentMessageID(result tg.UpdatesClass, randomID int64) int {
	var updates []tg.UpdateClass
	switch v := result.(type) {
	case *tg.UpdateShortSentMessage:
		return v.ID
	case *tg.Updates:
		updates = v.Updates
	case *tg.UpdatesCombined:
		updates = v.Updates
	}
	for _, u := range updates {
		if m, ok := u.(*tg.UpdateMessageID); ok && m.RandomID == randomID {
			return m.ID
		}
	}
	return 0
}

// encodeMessage builds a sendMessage request (without peer or random_id) from
// a message with one text part. Other content is not supported yet.
func encodeMessage(m otogi.OutboundMessage) (*tg.MessagesSendMessageRequest, error) {
	parts := m.Message.Parts
	if len(parts) != 1 || parts[0].Kind != otogi.PartText || len(parts[0].Children) != 0 {
		return nil, fmt.Errorf("telegram sender supports one text part: %w", otogi.ErrUnsupported)
	}
	text := parts[0].Text
	req := &tg.MessagesSendMessageRequest{Message: text}
	var entities []tg.MessageEntityClass
	for _, a := range parts[0].Annotations {
		e, err := encodeEntity(text, a)
		if err != nil {
			return nil, err
		}
		entities = append(entities, e)
	}
	if len(entities) > 0 {
		req.SetEntities(entities)
	}
	var reply, thread int
	for _, r := range m.Message.Relations {
		if r.Kind != otogi.RelationReply && r.Kind != otogi.RelationThread {
			return nil, fmt.Errorf("telegram relation %s: %w", r.Kind, otogi.ErrUnsupported)
		}
		l, err := mapper.DecodeMessageLocator(r.Target.Locator.Data)
		if err != nil || l.Peer != string(m.Conversation) {
			return nil, fmt.Errorf("telegram relation target is not a message in this conversation: %w", otogi.ErrUnsupported)
		}
		if r.Kind == otogi.RelationReply {
			reply = l.MessageID
		} else {
			thread = l.MessageID
		}
	}
	if reply != 0 || thread != 0 {
		if reply == 0 {
			reply = thread
		}
		replyTo := &tg.InputReplyToMessage{ReplyToMsgID: reply}
		if thread != 0 && thread != reply {
			replyTo.SetTopMsgID(thread)
		}
		req.SetReplyTo(replyTo)
	}
	return req, nil
}

func encodeEntity(text string, a otogi.Annotation) (tg.MessageEntityClass, error) {
	off, length := utf16Len(text[:a.Offset]), utf16Len(text[a.Offset:a.Offset+a.Length])
	switch a.Kind {
	case otogi.AnnotationBold:
		return &tg.MessageEntityBold{Offset: off, Length: length}, nil
	case otogi.AnnotationItalic:
		return &tg.MessageEntityItalic{Offset: off, Length: length}, nil
	case otogi.AnnotationUnderline:
		return &tg.MessageEntityUnderline{Offset: off, Length: length}, nil
	case otogi.AnnotationStrike:
		return &tg.MessageEntityStrike{Offset: off, Length: length}, nil
	case otogi.AnnotationSpoiler:
		return &tg.MessageEntitySpoiler{Offset: off, Length: length}, nil
	case otogi.AnnotationCode:
		return &tg.MessageEntityCode{Offset: off, Length: length}, nil
	case otogi.AnnotationPreformatted:
		return &tg.MessageEntityPre{Offset: off, Length: length, Language: a.Target}, nil
	case otogi.AnnotationBlockQuote:
		return &tg.MessageEntityBlockquote{Offset: off, Length: length}, nil
	case otogi.AnnotationLink:
		if a.Target == "" {
			return &tg.MessageEntityURL{Offset: off, Length: length}, nil
		}
		return &tg.MessageEntityTextURL{Offset: off, Length: length, URL: a.Target}, nil
	default:
		return nil, fmt.Errorf("telegram annotation %s: %w", a.Kind, otogi.ErrUnsupported)
	}
}

func utf16Len(s string) int { return len(utf16.Encode([]rune(s))) }

// parsePeer splits a conversation ID from mapper.PeerID ("user:7").
func parsePeer(conversation string) (kind string, id int64, err error) {
	kind, raw, _ := strings.Cut(conversation, ":")
	id, err = strconv.ParseInt(raw, 10, 64)
	if err != nil || (kind != "user" && kind != "chat" && kind != "channel") {
		return "", 0, fmt.Errorf("telegram: invalid conversation %q: %w", conversation, otogi.ErrInvalid)
	}
	return kind, id, nil
}

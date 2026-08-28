package telegram

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/telegram/mapper"
	telegramstore "github.com/TundraWork/Ex-Otogi/internal/adapter/telegram/store"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

const sendCodecVersion uint16 = 1

// ErrAmbiguousSend reports that Telegram may have accepted the durable intent and normal Send must not retry it.
var ErrAmbiguousSend = errors.New("telegram: ambiguous send outcome")

type sendAPI interface {
	MessagesSendMessage(context.Context, *tg.MessagesSendMessageRequest) (tg.UpdatesClass, error)
}
type sendJournal interface {
	Handle(context.Context, tg.UpdatesClass) error
}
type sendStore interface {
	CreateSendIntent(context.Context, telegramstore.SendIntent, time.Time) (telegramstore.SendIntent, bool, error)
	GetSendIntent(context.Context, string) (telegramstore.SendIntent, bool, error)
	TransitionSend(context.Context, string, telegramstore.SendStatus, telegramstore.SendStatus, telegramstore.SendTransition) error
	GetPeer(context.Context, string, int64) (telegramstore.Peer, bool, error)
	RecoverableSends(context.Context, time.Time) ([]telegramstore.SendIntent, error)
}

// RateLimitError reports exact Telegram send eligibility without sleeping or retrying.
type RateLimitError struct {
	// RetryAt is the exact UTC instant when the same logical send becomes eligible again.
	RetryAt time.Time
	// Cause retains the Telegram RPC error for errors.Is and errors.As classification.
	Cause error
}

// Error describes the exact retry instant without exposing any implicit retry behavior.
func (e *RateLimitError) Error() string {
	return fmt.Sprintf("telegram: rate limited until %s: %v", e.RetryAt.Format(time.RFC3339), e.Cause)
}

// Unwrap returns the underlying Telegram RPC error for identity-preserving classification.
func (e *RateLimitError) Unwrap() error { return e.Cause }

// Sender performs validated, durable, idempotent raw Telegram sends.
type Sender struct {
	api     sendAPI
	store   sendStore
	journal sendJournal
	now     func() time.Time
}

// NewSender constructs a sender that validates content before durable mutation and journals every successful RPC result before reporting success.
func NewSender(api sendAPI, store sendStore, journal sendJournal, now func() time.Time) *Sender {
	if api == nil || store == nil || journal == nil || now == nil {
		panic("telegram: nil sender dependency")
	}
	return &Sender{api: api, store: store, journal: journal, now: now}
}

// Reconcile conservatively records pre-crash in-flight RPC uncertainty.
func (s *Sender) Reconcile(ctx context.Context) error {
	now := s.now().UTC()
	intents, err := s.store.RecoverableSends(ctx, now)
	if err != nil {
		return err
	}
	for _, intent := range intents {
		if intent.Status != telegramstore.SendInFlight {
			continue
		}
		if err := s.store.TransitionSend(ctx, intent.Key, telegramstore.SendInFlight, telegramstore.SendAmbiguous, telegramstore.SendTransition{At: now, Error: "restart during RPC"}); err != nil {
			return err
		}
	}
	return nil
}

// RandomID derives a stable positive nonzero Telegram random_id from an idempotency key.
func RandomID(key string) int64 {
	h := sha256.New()
	var v [2]byte
	binary.BigEndian.PutUint16(v[:], sendCodecVersion)
	_, _ = h.Write(v[:])
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(key)))
	_, _ = h.Write(n[:])
	_, _ = h.Write([]byte(key))
	sum := h.Sum(nil)
	id := int64(binary.BigEndian.Uint64(sum[:8]) & (1<<63 - 1))
	if id == 0 {
		return 1
	}
	return id
}

// RequestDigest returns the explicitly versioned canonical logical request digest.
// Peer access hashes and random_id are excluded because they are durable transport material.
func RequestDigest(req *tg.MessagesSendMessageRequest) string {
	d, _ := requestDigest(req)
	return hex.EncodeToString(d)
}
func requestDigest(req *tg.MessagesSendMessageRequest) ([]byte, error) {
	if req == nil {
		return nil, errors.New("nil send request")
	}
	logical := *req
	switch peer := req.Peer.(type) {
	case *tg.InputPeerUser:
		logical.Peer = &tg.InputPeerUser{UserID: peer.UserID}
	case *tg.InputPeerChannel:
		logical.Peer = &tg.InputPeerChannel{ChannelID: peer.ChannelID}
	case *tg.InputPeerChat:
		logical.Peer = &tg.InputPeerChat{ChatID: peer.ChatID}
	default:
		return nil, errors.New("telegram: invalid logical peer")
	}
	logical.RandomID = 0
	var b bin.Buffer
	if err := logical.Encode(&b); err != nil {
		return nil, err
	}
	hash := sha256.Sum256(b.Raw())
	digest := make([]byte, 2+len(hash))
	binary.BigEndian.PutUint16(digest, 2)
	copy(digest[2:], hash[:])
	return digest, nil
}

// Send durably executes one idempotent rich-text intent; ambiguous or in-flight redelivery returns ErrAmbiguousSend without another RPC.
func (s *Sender) Send(ctx context.Context, message otogi.OutboundMessage) error {
	template, err := encodeMessage(message)
	if err != nil {
		return err
	}
	digest, err := requestDigest(template)
	if err != nil {
		return fmt.Errorf("telegram: digest request: %w", err)
	}
	now := s.now().UTC()
	key := message.IdempotencyKey().String()
	intent, found, err := s.store.GetSendIntent(ctx, key)
	if err != nil {
		return err
	}
	if found {
		if !bytes.Equal(intent.RequestDigest, digest) {
			return telegramstore.ErrSendConflict
		}
		switch intent.Status {
		case telegramstore.SendSucceeded:
			return nil
		case telegramstore.SendPermanentFailed:
			return fmt.Errorf("telegram: prior permanent send failure: %s", intent.Error)
		case telegramstore.SendInFlight, telegramstore.SendAmbiguous:
			return ErrAmbiguousSend
		case telegramstore.SendRetryable:
			if intent.RetryAt != nil && now.Before(*intent.RetryAt) {
				return &RateLimitError{RetryAt: *intent.RetryAt, Cause: errors.New(intent.Error)}
			}
		}
	} else {
		peer, record, peerErr := s.inputPeer(ctx, message.Conversation().String())
		if peerErr != nil {
			return peerErr
		}
		locator := encodePeerRecord(record)
		var created bool
		intent, created, err = s.store.CreateSendIntent(ctx, telegramstore.SendIntent{Key: key, RequestDigest: digest, RandomID: RandomID(key), PeerLocator: locator}, now)
		if err != nil {
			return err
		}
		if !created {
			found = true
			if !bytes.Equal(intent.RequestDigest, digest) {
				return telegramstore.ErrSendConflict
			}
			switch intent.Status {
			case telegramstore.SendSucceeded:
				return nil
			case telegramstore.SendPermanentFailed:
				return fmt.Errorf("telegram: prior permanent send failure: %s", intent.Error)
			case telegramstore.SendInFlight, telegramstore.SendAmbiguous:
				return ErrAmbiguousSend
			case telegramstore.SendRetryable:
				if intent.RetryAt != nil && now.Before(*intent.RetryAt) {
					return &RateLimitError{RetryAt: *intent.RetryAt, Cause: errors.New(intent.Error)}
				}
			}
		} else {
			template.Peer = peer
			template.RandomID = intent.RandomID
		}
	}
	if found {
		peer, peerErr := decodePeerRecord(intent.PeerLocator)
		if peerErr != nil {
			return peerErr
		}
		template.Peer = peer
		template.RandomID = intent.RandomID
	}
	if err = s.store.TransitionSend(ctx, key, intent.Status, telegramstore.SendInFlight, telegramstore.SendTransition{At: now}); err != nil {
		return err
	}
	updates, rpcErr := s.api.MessagesSendMessage(ctx, template)
	completedAt := s.now().UTC()
	persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if rpcErr != nil {
		return s.recordSendError(persist, key, rpcErr, completedAt)
	}
	if updates == nil {
		e := errors.New("telegram: nil send updates")
		if transitionErr := s.store.TransitionSend(persist, key, telegramstore.SendInFlight, telegramstore.SendAmbiguous, telegramstore.SendTransition{At: completedAt, Error: "nil updates"}); transitionErr != nil {
			return errors.Join(e, transitionErr)
		}
		return e
	}
	if err = s.journal.Handle(persist, updates); err != nil {
		transitionErr := s.store.TransitionSend(persist, key, telegramstore.SendInFlight, telegramstore.SendAmbiguous, telegramstore.SendTransition{At: completedAt, Error: "journal returned updates"})
		return errors.Join(fmt.Errorf("telegram: journal returned updates: %w", err), transitionErr)
	}
	id := correlatedMessageID(updates, template.RandomID)
	if id <= 0 {
		e := errors.New("telegram: missing message correlation")
		if transitionErr := s.store.TransitionSend(persist, key, telegramstore.SendInFlight, telegramstore.SendAmbiguous, telegramstore.SendTransition{At: completedAt, Error: "missing message correlation"}); transitionErr != nil {
			return errors.Join(e, transitionErr)
		}
		return e
	}
	current, found, loadErr := s.store.GetSendIntent(persist, key)
	if loadErr != nil {
		return loadErr
	}
	if found && current.Status == telegramstore.SendSucceeded {
		return nil
	}
	return s.store.TransitionSend(persist, key, telegramstore.SendInFlight, telegramstore.SendSucceeded, telegramstore.SendTransition{At: completedAt, ProviderMessageID: int64(id)})
}

func (s *Sender) recordSendError(ctx context.Context, key string, cause error, at time.Time) error {
	if rpc, ok := tgerr.As(cause); ok && (rpc.Type == "FLOOD_WAIT" || rpc.Type == "SLOWMODE_WAIT") && rpc.Argument >= 0 {
		retry := at.Add(time.Duration(rpc.Argument) * time.Second)
		if err := s.store.TransitionSend(ctx, key, telegramstore.SendInFlight, telegramstore.SendRetryable, telegramstore.SendTransition{At: at, RetryAt: &retry, Error: rpc.Type}); err != nil {
			return errors.Join(cause, err)
		}
		return &RateLimitError{RetryAt: retry, Cause: cause}
	}
	to := telegramstore.SendPermanentFailed
	label := "rpc failure"
	if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		to = telegramstore.SendAmbiguous
		label = "rpc canceled"
	} else if rpc, ok := tgerr.As(cause); !ok || rpc.Code >= 500 {
		to = telegramstore.SendAmbiguous
		label = "rpc uncertainty"
	}
	if err := s.store.TransitionSend(ctx, key, telegramstore.SendInFlight, to, telegramstore.SendTransition{At: at, Error: label}); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

func encodeMessage(m otogi.OutboundMessage) (*tg.MessagesSendMessageRequest, error) {
	parts := m.Message().Parts()
	roots := m.Message().Roots()
	if len(parts) != 1 || len(roots) != 1 || parts[0].Kind() != otogi.PartText || len(parts[0].Children()) != 0 || len(parts[0].Extensions()) != 0 || len(m.Message().Extensions()) != 0 {
		return nil, fmt.Errorf("telegram rich text codec: %w", otogi.ErrUnsupported)
	}
	text, anns, _ := parts[0].Text()
	entities := make([]tg.MessageEntityClass, 0, len(anns))
	for _, a := range anns {
		e, err := encodeEntity(text, a)
		if err != nil {
			return nil, err
		}
		entities = append(entities, e)
	}
	var reply, thread int
	for _, r := range m.Message().Relations() {
		if len(r.Extensions()) != 0 || r.Key() != "" || (r.Kind() != otogi.RelationReply && r.Kind() != otogi.RelationThread) || !r.Target().HasLocator() {
			return nil, fmt.Errorf("telegram relation: %w", otogi.ErrUnsupported)
		}
		l := r.Target().Locator()
		if !strings.HasSuffix(l.NamespaceURI(), "/message") || l.SchemaVersion() != 1 {
			return nil, fmt.Errorf("telegram relation locator: %w", otogi.ErrUnsupported)
		}
		ml, e := mapper.DecodeMessageLocator(l.Data())
		if e != nil || ml.Peer != m.Conversation().String() {
			return nil, fmt.Errorf("telegram relation locator: %w", otogi.ErrUnsupported)
		}
		if r.Kind() == otogi.RelationReply {
			if reply != 0 {
				return nil, fmt.Errorf("multiple replies: %w", otogi.ErrUnsupported)
			}
			reply = ml.MessageID
		} else {
			if thread != 0 {
				return nil, fmt.Errorf("multiple threads: %w", otogi.ErrUnsupported)
			}
			thread = ml.MessageID
		}
	}
	peer, err := logicalPeer(m.Conversation().String())
	if err != nil {
		return nil, err
	}
	req := &tg.MessagesSendMessageRequest{Peer: peer, Message: text}
	if len(entities) > 0 {
		req.SetEntities(entities)
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

func logicalPeer(conversation string) (tg.InputPeerClass, error) {
	kind, id, err := parsePeer(conversation)
	if err != nil {
		return nil, err
	}
	switch kind {
	case "user":
		return &tg.InputPeerUser{UserID: id}, nil
	case "chat":
		return &tg.InputPeerChat{ChatID: id}, nil
	case "channel":
		return &tg.InputPeerChannel{ChannelID: id}, nil
	default:
		return nil, errors.New("telegram: invalid logical peer")
	}
}

func encodeEntity(text string, a otogi.Annotation) (tg.MessageEntityClass, error) {
	if len(a.Extensions()) != 0 {
		return nil, fmt.Errorf("telegram annotation extension: %w", otogi.ErrUnsupported)
	}
	if a.Kind() != otogi.AnnotationLink && a.Kind() != otogi.AnnotationPreformatted && a.Target() != "" {
		return nil, fmt.Errorf("telegram annotation target: %w", otogi.ErrUnsupported)
	}
	off, length, ok := utf16Range(text, a.Offset(), a.Length())
	if !ok {
		return nil, fmt.Errorf("telegram annotation range: %w", otogi.ErrUnsupported)
	}
	switch a.Kind() {
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
		return &tg.MessageEntityPre{Offset: off, Length: length, Language: a.Target()}, nil
	case otogi.AnnotationBlockQuote:
		return &tg.MessageEntityBlockquote{Offset: off, Length: length}, nil
	case otogi.AnnotationLink:
		if a.Target() == "" {
			return &tg.MessageEntityURL{Offset: off, Length: length}, nil
		}
		return &tg.MessageEntityTextURL{Offset: off, Length: length, URL: a.Target()}, nil
	default:
		return nil, fmt.Errorf("telegram annotation %s: %w", a.Kind(), otogi.ErrUnsupported)
	}
}
func utf16Range(s string, byteOffset, byteLength int) (int, int, bool) {
	if byteOffset < 0 || byteLength < 0 || byteOffset+byteLength > len(s) || !utf8.ValidString(s[:byteOffset]) || !utf8.ValidString(s[byteOffset:byteOffset+byteLength]) {
		return 0, 0, false
	}
	return len(utf16.Encode([]rune(s[:byteOffset]))), len(utf16.Encode([]rune(s[byteOffset : byteOffset+byteLength]))), true
}
func (s *Sender) inputPeer(ctx context.Context, conversation string) (tg.InputPeerClass, telegramstore.Peer, error) {
	kind, id, err := parsePeer(conversation)
	if err != nil {
		return nil, telegramstore.Peer{}, err
	}
	record, ok, err := s.store.GetPeer(ctx, kind, id)
	if err != nil {
		return nil, record, err
	}
	if !ok {
		return nil, record, fmt.Errorf("telegram: peer %s not found", conversation)
	}
	switch kind {
	case "user":
		if record.AccessHash == 0 {
			return nil, record, errors.New("telegram: user access hash missing")
		}
		return &tg.InputPeerUser{UserID: id, AccessHash: record.AccessHash}, record, nil
	case "chat":
		return &tg.InputPeerChat{ChatID: id}, record, nil
	case "channel":
		if record.AccessHash == 0 {
			return nil, record, errors.New("telegram: channel access hash missing")
		}
		return &tg.InputPeerChannel{ChannelID: id, AccessHash: record.AccessHash}, record, nil
	}
	panic("unreachable")
}
func parsePeer(value string) (string, int64, error) {
	kind, raw, ok := strings.Cut(value, ":")
	if !ok || (kind != "user" && kind != "chat" && kind != "channel") {
		return "", 0, fmt.Errorf("telegram: invalid peer %q", value)
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return "", 0, fmt.Errorf("telegram: invalid peer %q", value)
	}
	return kind, id, nil
}
func encodePeerRecord(p telegramstore.Peer) []byte {
	return []byte(fmt.Sprintf("%d:%s:%d:%d", sendCodecVersion, p.Kind, p.ID, p.AccessHash))
}
func decodePeerRecord(data []byte) (tg.InputPeerClass, error) {
	fields := strings.Split(string(data), ":")
	if len(fields) != 4 || fields[0] != strconv.Itoa(int(sendCodecVersion)) {
		return nil, errors.New("telegram: invalid durable peer locator")
	}
	id, idErr := strconv.ParseInt(fields[2], 10, 64)
	hash, hashErr := strconv.ParseInt(fields[3], 10, 64)
	if idErr != nil || hashErr != nil || id <= 0 {
		return nil, errors.New("telegram: invalid durable peer locator")
	}
	switch fields[1] {
	case "user":
		if hash == 0 {
			break
		}
		return &tg.InputPeerUser{UserID: id, AccessHash: hash}, nil
	case "chat":
		return &tg.InputPeerChat{ChatID: id}, nil
	case "channel":
		if hash == 0 {
			break
		}
		return &tg.InputPeerChannel{ChannelID: id, AccessHash: hash}, nil
	}
	return nil, errors.New("telegram: invalid durable peer locator")
}
func correlatedMessageID(packet tg.UpdatesClass, randomID int64) int {
	switch v := packet.(type) {
	case *tg.Updates:
		for _, u := range v.Updates {
			if x, ok := u.(*tg.UpdateMessageID); ok && x.RandomID == randomID {
				return x.ID
			}
		}
	case *tg.UpdatesCombined:
		for _, u := range v.Updates {
			if x, ok := u.(*tg.UpdateMessageID); ok && x.RandomID == randomID {
				return x.ID
			}
		}
	case *tg.UpdateShortSentMessage:
		return v.ID
	}
	return 0
}

package telegram

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/telegram/mapper"
	telegramstore "github.com/TundraWork/Ex-Otogi/internal/adapter/telegram/store"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// MaxMediaBytes bounds one media download.
const MaxMediaBytes = 50 << 20

type sendStore interface {
	FindSent(context.Context, string) (telegramstore.Sent, bool, error)
	PutSent(context.Context, telegramstore.Sent, time.Time) error
}

// effects performs effects on Telegram. A send happens at most once per
// idempotency key: a recorded key returns the original message, and a resend
// after a crash reuses the same random_id, which Telegram rejects as a
// duplicate. Edit, delete, and react are naturally idempotent. RPC results
// reach the update manager through the client's update hook middleware.
type effects struct {
	api     *tg.Client
	store   sendStore
	peers   func(context.Context, string) (tg.InputPeerClass, error)
	channel func(context.Context, int64) (tg.InputChannelClass, error)
	ref     func(conversation string, messageID int) otogi.EventRef
	log     *slog.Logger
	now     func() time.Time
}

func (x *effects) Apply(ctx context.Context, e otogi.Effect) (otogi.EventRef, error) {
	switch e.Kind {
	case otogi.EffectSend:
		return x.send(ctx, e)
	case otogi.EffectEdit, otogi.EffectDelete, otogi.EffectReact:
		conversation, id, err := messageTarget(e.Ref)
		if err != nil {
			return otogi.EventRef{}, err
		}
		switch e.Kind {
		case otogi.EffectEdit:
			err = x.edit(ctx, conversation, id, e.Message)
		case otogi.EffectDelete:
			err = x.delete(ctx, conversation, id)
		default:
			err = x.react(ctx, conversation, id, e.Reaction)
		}
		return x.ref(conversation, id), err
	default:
		return otogi.EventRef{}, fmt.Errorf("telegram: effect %q: %w", e.Kind, otogi.ErrUnsupported)
	}
}

func (x *effects) send(ctx context.Context, e otogi.Effect) (otogi.EventRef, error) {
	key, conversation := string(e.Key), string(e.To.Conversation)
	if sent, found, err := x.store.FindSent(ctx, key); err != nil {
		return otogi.EventRef{}, err
	} else if found {
		return x.ref(sent.Conversation, sent.MessageID), nil
	}
	content, err := encodeContent(e.Message, conversation)
	if err != nil {
		return otogi.EventRef{}, err
	}
	peer, err := x.peers(ctx, conversation)
	if err != nil {
		return otogi.EventRef{}, fmt.Errorf("telegram send: resolve %s: %w", conversation, err)
	}
	randomID := RandomID(key)
	call := func() (tg.UpdatesClass, error) {
		if content.media == nil {
			req := &tg.MessagesSendMessageRequest{Peer: peer, Message: content.text, RandomID: randomID}
			content.apply(req.SetEntities, req.SetReplyTo)
			return x.api.MessagesSendMessage(ctx, req)
		}
		media, err := x.inputMedia(ctx, content.media)
		if err != nil {
			return nil, err
		}
		req := &tg.MessagesSendMediaRequest{Peer: peer, Media: media, Message: content.text, RandomID: randomID}
		content.apply(req.SetEntities, req.SetReplyTo)
		return x.api.MessagesSendMedia(ctx, req)
	}
	result, err := withFloodWait(ctx, call)
	switch {
	case tgerr.Is(err, "RANDOM_ID_DUPLICATE"):
		// An earlier attempt was delivered but not recorded before a crash.
		return x.ref(conversation, 0), nil
	case err != nil:
		return otogi.EventRef{}, fmt.Errorf("telegram send: %w", err)
	}
	id := sentMessageID(result, randomID)
	// A failed record is not fatal: a replay resends with the same random_id
	// and Telegram reports the duplicate.
	if err := x.store.PutSent(ctx, telegramstore.Sent{Key: key, Conversation: conversation, MessageID: id}, x.now()); err != nil {
		x.log.Error("telegram: record sent message", "key", key, "error", err)
	}
	return x.ref(conversation, id), nil
}

func (x *effects) edit(ctx context.Context, conversation string, id int, msg otogi.Message) error {
	content, err := encodeContent(msg, conversation)
	if err != nil {
		return err
	}
	if content.media != nil || content.replyTo != nil {
		return fmt.Errorf("telegram edit supports text only: %w", otogi.ErrUnsupported)
	}
	peer, err := x.peers(ctx, conversation)
	if err != nil {
		return err
	}
	req := &tg.MessagesEditMessageRequest{Peer: peer, ID: id}
	req.SetMessage(content.text)
	req.SetEntities(content.entities)
	_, err = withFloodWait(ctx, func() (tg.UpdatesClass, error) { return x.api.MessagesEditMessage(ctx, req) })
	if tgerr.Is(err, "MESSAGE_NOT_MODIFIED") {
		return nil // already applied
	}
	return wrap("edit", err)
}

func (x *effects) delete(ctx context.Context, conversation string, id int) error {
	kind, peerID, err := parsePeer(conversation)
	if err != nil {
		return err
	}
	if kind == "channel" {
		channel, err := x.channel(ctx, peerID)
		if err != nil {
			return err
		}
		_, err = x.api.ChannelsDeleteMessages(ctx, &tg.ChannelsDeleteMessagesRequest{Channel: channel, ID: []int{id}})
		return wrap("delete", err)
	}
	// Deleting a message that no longer exists succeeds with no effect.
	_, err = x.api.MessagesDeleteMessages(ctx, &tg.MessagesDeleteMessagesRequest{Revoke: true, ID: []int{id}})
	return wrap("delete", err)
}

func (x *effects) react(ctx context.Context, conversation string, id int, reaction string) error {
	peer, err := x.peers(ctx, conversation)
	if err != nil {
		return err
	}
	req := &tg.MessagesSendReactionRequest{Peer: peer, MsgID: id}
	if reaction != "" {
		req.SetReaction([]tg.ReactionClass{&tg.ReactionEmoji{Emoticon: reaction}})
	}
	_, err = x.api.MessagesSendReaction(ctx, req)
	return wrap("react", err)
}

// FetchMedia downloads Telegram media described by a mapper.MediaLocator.
func (x *effects) FetchMedia(ctx context.Context, src otogi.MediaSource) ([]byte, error) {
	if src.Kind != otogi.MediaSourceOpaque || !strings.HasSuffix(src.Locator.Namespace, "/media") {
		return nil, fmt.Errorf("telegram: not a Telegram media source: %w", otogi.ErrUnsupported)
	}
	l, err := mapper.DecodeMediaLocator(src.Locator.Data)
	if err != nil {
		return nil, fmt.Errorf("telegram fetch: %w: %w", otogi.ErrInvalid, err)
	}
	var location tg.InputFileLocationClass
	switch l.Kind {
	case "photo":
		location = &tg.InputPhotoFileLocation{ID: l.ID, AccessHash: l.AccessHash, FileReference: l.FileReference, ThumbSize: l.ThumbSize}
	case "document":
		location = &tg.InputDocumentFileLocation{ID: l.ID, AccessHash: l.AccessHash, FileReference: l.FileReference}
	default:
		return nil, fmt.Errorf("telegram fetch: media kind %q: %w", l.Kind, otogi.ErrUnsupported)
	}
	var buf limitedBuffer
	if _, err := downloader.NewDownloader().Download(x.api, location).Stream(ctx, &buf); err != nil {
		return nil, fmt.Errorf("telegram fetch: %w", err)
	}
	return buf.Bytes(), nil
}

// limitedBuffer fails writes beyond MaxMediaBytes.
type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > MaxMediaBytes {
		return 0, fmt.Errorf("media exceeds %d bytes", MaxMediaBytes)
	}
	return b.Buffer.Write(p)
}

func (x *effects) inputMedia(ctx context.Context, m *otogi.Media) (tg.InputMediaClass, error) {
	photo := m.Role == otogi.MediaImage
	switch m.Source.Kind {
	case otogi.MediaSourceInline:
		name := m.Filename
		if name == "" {
			name = "file"
		}
		file, err := uploader.NewUploader(x.api).FromBytes(ctx, name, m.Source.Inline)
		if err != nil {
			return nil, fmt.Errorf("telegram upload: %w", err)
		}
		if photo {
			return &tg.InputMediaUploadedPhoto{File: file}, nil
		}
		mime := m.MediaType
		if mime == "" {
			mime = "application/octet-stream"
		}
		return &tg.InputMediaUploadedDocument{File: file, MimeType: mime, Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: name}}}, nil
	case otogi.MediaSourceExternal:
		if photo {
			return &tg.InputMediaPhotoExternal{URL: m.Source.URL}, nil
		}
		return &tg.InputMediaDocumentExternal{URL: m.Source.URL}, nil
	case otogi.MediaSourceOpaque:
		l, err := mapper.DecodeMediaLocator(m.Source.Locator.Data)
		if err != nil {
			return nil, fmt.Errorf("telegram media locator: %w: %w", otogi.ErrInvalid, err)
		}
		if l.Kind == "photo" {
			return &tg.InputMediaPhoto{ID: &tg.InputPhoto{ID: l.ID, AccessHash: l.AccessHash, FileReference: l.FileReference}}, nil
		}
		return &tg.InputMediaDocument{ID: &tg.InputDocument{ID: l.ID, AccessHash: l.AccessHash, FileReference: l.FileReference}}, nil
	default:
		return nil, fmt.Errorf("telegram media source %q: %w", m.Source.Kind, otogi.ErrUnsupported)
	}
}

// content is a message encoded for Telegram: text (or a media caption) with
// entities, at most one media part, and an optional reply.
type content struct {
	text     string
	entities []tg.MessageEntityClass
	media    *otogi.Media
	replyTo  *tg.InputReplyToMessage
}

func (c content) apply(setEntities func([]tg.MessageEntityClass), setReplyTo func(tg.InputReplyToClass)) {
	if len(c.entities) > 0 {
		setEntities(c.entities)
	}
	if c.replyTo != nil {
		setReplyTo(c.replyTo)
	}
}

// encodeContent accepts at most one text part and one media part, in any
// order, without nesting.
func encodeContent(m otogi.Message, conversation string) (content, error) {
	var c content
	for _, part := range m.Parts {
		switch {
		case len(part.Children) > 0:
			return content{}, fmt.Errorf("telegram: nested parts: %w", otogi.ErrUnsupported)
		case part.Kind == otogi.PartText && c.text == "":
			c.text = part.Text
			for _, a := range part.Annotations {
				e, err := encodeEntity(part.Text, a)
				if err != nil {
					return content{}, err
				}
				c.entities = append(c.entities, e)
			}
		case (part.Kind == otogi.PartMedia || part.Kind == otogi.PartFile) && c.media == nil:
			c.media = part.Media
		default:
			return content{}, fmt.Errorf("telegram: one text and one media part at most, got %s: %w", part.Kind, otogi.ErrUnsupported)
		}
	}
	var reply, thread int
	for _, r := range m.Relations {
		if r.Kind != otogi.RelationReply && r.Kind != otogi.RelationThread {
			return content{}, fmt.Errorf("telegram relation %s: %w", r.Kind, otogi.ErrUnsupported)
		}
		peer, id, err := messageTarget(r.Target)
		if err != nil || peer != conversation {
			return content{}, fmt.Errorf("telegram relation target is not a message in this conversation: %w", otogi.ErrUnsupported)
		}
		if r.Kind == otogi.RelationReply {
			reply = id
		} else {
			thread = id
		}
	}
	if reply != 0 || thread != 0 {
		if reply == 0 {
			reply = thread
		}
		c.replyTo = &tg.InputReplyToMessage{ReplyToMsgID: reply}
		if thread != 0 && thread != reply {
			c.replyTo.SetTopMsgID(thread)
		}
	}
	return c, nil
}

// messageTarget returns the conversation and message ID a reference points at.
func messageTarget(ref otogi.EventRef) (string, int, error) {
	if ref.Platform != Platform || !strings.HasSuffix(ref.Locator.Namespace, "/message") {
		return "", 0, fmt.Errorf("telegram: reference does not locate a Telegram message: %w", otogi.ErrInvalid)
	}
	l, err := mapper.DecodeMessageLocator(ref.Locator.Data)
	if err != nil {
		return "", 0, fmt.Errorf("telegram: message locator: %w: %w", otogi.ErrInvalid, err)
	}
	return l.Peer, l.MessageID, nil
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

// withFloodWait calls f, and once more after waiting out a FLOOD_WAIT.
func withFloodWait(ctx context.Context, f func() (tg.UpdatesClass, error)) (tg.UpdatesClass, error) {
	result, err := f()
	if waited, waitErr := tgerr.FloodWait(ctx, err); waited {
		return f()
	} else if waitErr != nil {
		return nil, waitErr
	}
	return result, nil
}

func wrap(op string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("telegram %s: %w", op, err)
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

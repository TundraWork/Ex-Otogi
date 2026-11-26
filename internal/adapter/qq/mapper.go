package qq

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	qqstore "github.com/TundraWork/Ex-Otogi/internal/adapter/qq/store"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// extensionNamespace marks QQ payloads kept on events.
const extensionNamespace = "qq"

// message is a C2C or group message dispatch.
type message struct {
	ID          string `json:"id"`
	Content     string `json:"content"`
	Timestamp   string `json:"timestamp"`
	GroupOpenID string `json:"group_openid"`
	Author      struct {
		ID           string `json:"id"`
		UserOpenID   string `json:"user_openid"`
		MemberOpenID string `json:"member_openid"`
		UnionOpenID  string `json:"union_openid"`
		Username     string `json:"username"`
		// MemberNickname is the member's group card: the name this group
		// shows, where Username is their QQ nickname.
		MemberNickname string `json:"member_nickname"`
		// MemberRole is the member's standing in the group: owner, admin,
		// or member.
		MemberRole string `json:"member_role"`
		Bot        bool   `json:"bot"`
	} `json:"author"`
	Mentions []struct {
		ID             string `json:"id"`
		MemberOpenID   string `json:"member_openid"`
		UserOpenID     string `json:"user_openid"`
		Username       string `json:"username"`
		MemberNickname string `json:"member_nickname"`
		IsYou          bool   `json:"is_you"`
	} `json:"mentions"`
	Attachments []attachment `json:"attachments"`
	MessageType int          `json:"message_type"`
	Scene       struct {
		Ext []string `json:"ext"`
	} `json:"message_scene"`
	Elements json.RawMessage `json:"msg_elements"`
}

type attachment struct {
	URL         string `json:"url"`
	Filename    string `json:"filename"`
	Size        int64  `json:"size"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	ContentType string `json:"content_type"`
	VoiceWavURL string `json:"voice_wav_url"`
	ASRText     string `json:"asr_refer_text"`
}

// messageQuote is the message_type of a message that quotes another.
const messageQuote = 103

// sentType is the dispatch type of a message the bot sent, which the driver
// queues itself: QQ pushes none of the bot's own messages.
const sentType = "OTOGI_C2C_SENT"

// sentMessage is the payload of a sentType dispatch. Peer or Group names
// the chat.
type sentMessage struct {
	ID        string `json:"id"`
	Content   string `json:"content"` // as sent: text, or Markdown
	Timestamp string `json:"timestamp"`
	Peer      string `json:"peer,omitempty"`  // the user's openid, in C2C
	Group     string `json:"group,omitempty"` // the group's openid
	RefIdx    string `json:"ref_idx,omitempty"`
	Self      string `json:"self"`
	SelfName  string `json:"self_name,omitempty"`
}

func isMessage(t string) bool {
	return t == "C2C_MESSAGE_CREATE" || t == "GROUP_MESSAGE_CREATE" || t == "GROUP_AT_MESSAGE_CREATE"
}

// refs is what the mapper needs of the store: which message a REFIDX names.
type refs interface {
	PutRef(ctx context.Context, refIdx, messageID, conversation string, at time.Time) error
	FindRef(ctx context.Context, refIdx string) (string, bool, error)
}

// peerNames keeps the names users are seen with.
type peerNames interface {
	SetPeerName(ctx context.Context, peer, name string) error
	Peer(ctx context.Context, peer string) (qqstore.Peer, error)
}

// mapper turns dispatches into events.
type mapper struct {
	account otogi.AccountID
	refs    refs
	names   peerNames
	// self is the bot's own user ID from READY, which its own messages carry.
	self func() string
}

// mapFrame maps one inbox payload. Every dispatch becomes an event: messages
// as messages, everything else as a service event carrying the payload, so
// nothing QQ sends is lost.
func (m *mapper) mapFrame(ctx context.Context, f frame, received time.Time) (otogi.Event, error) {
	if f.T == sentType {
		return m.sent(f, received)
	}
	if !isMessage(f.T) {
		return m.service(f, received)
	}
	var msg message
	if err := json.Unmarshal(f.D, &msg); err != nil {
		return otogi.Event{}, fmt.Errorf("decode %s: %w", f.T, err)
	}
	e := otogi.Event{
		Platform: Platform, Account: m.account, SourceID: otogi.SourceEventID(msg.ID),
		Action: otogi.ActionMessageCreated, Direction: otogi.DirectionIncoming,
		ReceivedAt: received, Completeness: otogi.CompletenessComplete,
		Mentioned: f.T == "GROUP_AT_MESSAGE_CREATE", // QQ strips the @ itself
	}
	if t, err := time.Parse(time.RFC3339, msg.Timestamp); err == nil {
		e.OccurredAt = t.UTC()
	}
	actor := msg.Author.UserOpenID
	if f.T == "C2C_MESSAGE_CREATE" {
		e.Conversation = otogi.Conversation{ID: otogi.ConversationID("user:" + msg.Author.UserOpenID), Kind: otogi.ConversationDirect, Title: msg.Author.Username}
	} else {
		e.Conversation = otogi.Conversation{ID: otogi.ConversationID("group:" + msg.GroupOpenID), Kind: otogi.ConversationGroup}
		actor = msg.Author.MemberOpenID
		if msg.Author.Bot && msg.Author.ID != "" && msg.Author.ID == m.self() {
			e.Direction = otogi.DirectionOutgoing
		}
	}
	e.Actor = otogi.Actor{ID: otogi.ActorID("user:" + actor), Name: firstOf(msg.Author.MemberNickname, msg.Author.Username), Bot: msg.Author.Bot}
	if e.Conversation.Kind == otogi.ConversationGroup {
		e.Actor.Role = memberRole(msg.Author.MemberRole)
	}
	if err := m.learnNames(ctx, msg, actor); err != nil {
		return otogi.Event{}, err
	}

	var err error
	if e.Message, err = m.content(ctx, msg, &e); err != nil {
		return otogi.Event{}, err
	}
	for _, ext := range msg.Scene.Ext {
		if idx, ok := strings.CutPrefix(ext, "msg_idx="); ok {
			if err := m.refs.PutRef(ctx, idx, msg.ID, string(e.Conversation.ID), received); err != nil {
				return otogi.Event{}, err
			}
		}
	}
	e.Extensions = []otogi.Extension{{Namespace: extensionNamespace, Name: f.T, Version: 1, MediaType: "application/json", Data: f.D}}
	return e, nil
}

// sent maps a message the bot sent. It is not marked as a bot's: modules
// ignoring bots would not see it, and its direction says whose it is.
func (m *mapper) sent(f frame, received time.Time) (otogi.Event, error) {
	var s sentMessage
	if err := json.Unmarshal(f.D, &s); err != nil {
		return otogi.Event{}, fmt.Errorf("decode %s: %w", f.T, err)
	}
	var conversation otogi.Conversation
	switch {
	case s.Group != "":
		conversation = otogi.Conversation{ID: otogi.ConversationID("group:" + s.Group), Kind: otogi.ConversationGroup}
	case s.Peer != "":
		conversation = otogi.Conversation{ID: otogi.ConversationID("user:" + s.Peer), Kind: otogi.ConversationDirect}
	default:
		return otogi.Event{}, fmt.Errorf("decode %s: no peer or group", f.T)
	}
	e := otogi.Event{
		Platform: Platform, Account: m.account, SourceID: otogi.SourceEventID(s.ID),
		Conversation: conversation,
		Actor:        otogi.Actor{ID: otogi.ActorID("user:" + s.Self), Name: s.SelfName},
		Action:       otogi.ActionMessageCreated, Direction: otogi.DirectionOutgoing,
		ReceivedAt: received, Completeness: otogi.CompletenessComplete, Message: otogi.TextMessage(s.Content),
	}
	if t, err := time.Parse(time.RFC3339, s.Timestamp); err == nil {
		e.OccurredAt = t
	}
	return e, nil
}

// learnNames keeps the names a message shows: its sender's, and those of
// the people it mentions. QQ sends a name only now and then (never in
// private chats), so a name seen once names the user from then on. It keeps
// QQ nicknames, not group cards: a member's open ID is the same in every
// group and in private chats, so a group card would name them where it is
// not theirs.
func (m *mapper) learnNames(ctx context.Context, msg message, actor string) error {
	if msg.Author.Username != "" && actor != "" {
		if err := m.names.SetPeerName(ctx, "user:"+actor, msg.Author.Username); err != nil {
			return err
		}
	}
	for _, mention := range msg.Mentions {
		if id := firstOf(mention.MemberOpenID, mention.UserOpenID, mention.ID); id != "" && mention.Username != "" && !mention.IsYou {
			if err := m.names.SetPeerName(ctx, "user:"+id, mention.Username); err != nil {
				return err
			}
		}
	}
	return nil
}

// faceSticker is the face type of a sticker, whose picture comes as an
// attachment of the same message; faceMarket, of a market face, a sticker
// from QQ's store that comes as its name alone, with no picture or ID.
const (
	faceSticker = "6"
	faceMarket  = "4"
)

// face is the name a QQ face carries in its ext, base64 JSON whose text names
// it, such as "[进攻]"; a sticker's is empty. A face whose name cannot be
// read is "[表情]".
func face(ext string) string {
	var f struct {
		Text string `json:"text"`
	}
	if data, err := base64.StdEncoding.DecodeString(ext); err == nil && json.Unmarshal(data, &f) == nil {
		return f.Text
	}
	return "[表情]"
}

// tagToken is a tag QQ writes into message content: a mention, <@openid> or
// <@!openid>, or a face, <faceType=4,faceId="",ext="<base64 JSON>">.
var tagToken = regexp.MustCompile(`<@!?([^>\s]+)>|<faceType=(\d+),faceId="([^"]*)",ext="([^"]*)">`)

// content builds the message: text with mentions and faces as annotations,
// then the attachments, and a reply relation when it quotes another message.
// A face is its name, marked as an emoji whose target is qq:face:<type>, with
// :<id> when it has one; a market face (type 4), a sticker QQ gives only the
// name of, is marked as a sticker sent without its picture; a sticker's face
// leaves nothing, and its picture, which comes as an attachment, is a
// sticker.
func (m *mapper) content(ctx context.Context, msg message, e *otogi.Event) (otogi.Message, error) {
	var text otogi.RichText
	content, sticker := msg.Content, false
	if e.Mentioned {
		content = strings.TrimLeft(content, " ")
	}
	last := 0
	for _, loc := range tagToken.FindAllStringSubmatchIndex(content, -1) {
		text.Write(content[last:loc[0]])
		last = loc[1]
		if loc[2] < 0 {
			kind, id := content[loc[4]:loc[5]], content[loc[6]:loc[7]]
			sticker = sticker || kind == faceSticker
			if name := face(content[loc[8]:loc[9]]); name != "" {
				target := "qq:face:" + kind
				if id != "" {
					target += ":" + id
				}
				mark := otogi.AnnotationEmoji
				if kind == faceMarket {
					mark = otogi.AnnotationSticker
				}
				text.Mark(mark, target, func() { text.Write(name) })
			}
			continue
		}
		id := content[loc[2]:loc[3]]
		name, target := id, otogi.ActorID("user:"+id)
		if known, err := m.names.Peer(ctx, "user:"+id); err != nil {
			return otogi.Message{}, err
		} else if known.Name != "" {
			name = known.Name
		}
		for _, mention := range msg.Mentions {
			if mention.MemberOpenID == id || mention.UserOpenID == id || mention.ID == id {
				if n := firstOf(mention.MemberNickname, mention.Username); n != "" {
					name = n
				}
				if mention.IsYou {
					e.Mentioned = true
					target = otogi.ActorID("user:" + m.self())
				}
			}
		}
		if id == "all" {
			text.Write(content[loc[0]:loc[1]])
			continue
		}
		text.Mark(otogi.AnnotationMention, string(target), func() { text.Write("@" + name) })
	}
	text.Write(content[last:])
	for _, mention := range msg.Mentions {
		e.Mentioned = e.Mentioned || mention.IsYou
	}

	out := otogi.Message{}
	if text.Len() > 0 {
		out = text.Message()
	}
	for i, a := range msg.Attachments {
		part := attachmentPart(a)
		part.ID = otogi.PartID(fmt.Sprintf("attachment-%d", i))
		if sticker && part.Media != nil && part.Media.Role == otogi.MediaImage {
			part.Media.Role = otogi.MediaSticker
		}
		out.Parts = append(out.Parts, part)
		out.Roots = append(out.Roots, part.ID)
	}
	if msg.MessageType == messageQuote {
		rel, err := m.quote(ctx, msg, e)
		if err != nil {
			return otogi.Message{}, err
		}
		out.Relations = append(out.Relations, rel)
	}
	return out, nil
}

// quote is the reply relation of a message quoting another. The quoted
// message is found by its REFIDX; one the driver never saw keeps the REFIDX
// as its source event, and the quoted elements as an extension.
func (m *mapper) quote(ctx context.Context, msg message, e *otogi.Event) (otogi.Relation, error) {
	var refIdx string
	for _, ext := range msg.Scene.Ext {
		if idx, ok := strings.CutPrefix(ext, "ref_msg_idx="); ok {
			refIdx = idx
		}
	}
	rel := otogi.Relation{Kind: otogi.RelationReply, Target: otogi.EventRef{Platform: Platform, Account: m.account, Conversation: e.Conversation.ID, SourceEvent: otogi.SourceEventID(refIdx)}}
	if len(msg.Elements) > 0 {
		rel.Extensions = []otogi.Extension{{Namespace: extensionNamespace, Name: "msg_elements", Version: 1, MediaType: "application/json", Data: msg.Elements}}
	}
	if refIdx == "" {
		return rel, nil
	}
	id, ok, err := m.refs.FindRef(ctx, refIdx)
	if err != nil {
		return otogi.Relation{}, err
	}
	if ok {
		rel.Target.SourceEvent = otogi.SourceEventID(id)
	}
	return rel, nil
}

func attachmentPart(a attachment) otogi.Part {
	media := &otogi.Media{
		MediaType: a.ContentType, Filename: a.Filename, ByteSize: a.Size, Width: a.Width, Height: a.Height,
		Source: otogi.MediaSource{Kind: otogi.MediaSourceExternal, URL: a.URL},
	}
	if strings.HasPrefix(a.URL, "//") {
		media.Source.URL = "https:" + a.URL // older payloads leave out the scheme
	}
	kind := otogi.PartMedia
	switch {
	case strings.HasPrefix(a.ContentType, "image/"):
		media.Role = otogi.MediaImage
		if a.ContentType == "image/gif" {
			media.Role = otogi.MediaAnimation
		}
		if sum := filenameMD5(a.Filename); sum != nil {
			media.Hashes = []otogi.MediaHash{{Algorithm: "md5", Value: sum}}
		}
	case strings.HasPrefix(a.ContentType, "video/"):
		media.Role = otogi.MediaVideo
	case a.ContentType == "voice" || strings.HasPrefix(a.ContentType, "audio/"):
		media.Role, media.MediaType = otogi.MediaVoice, "audio/silk"
		if a.ContentType != "voice" {
			media.MediaType = a.ContentType
		}
		// QQ transcribes every voice message, whether or not the sender
		// asked it to show the text.
		media.Transcript = strings.TrimSpace(a.ASRText)
		voice, _ := json.Marshal(map[string]string{"wav_url": a.VoiceWavURL, "asr_text": a.ASRText})
		media.Extensions = []otogi.Extension{{Namespace: extensionNamespace, Name: "voice", Version: 1, MediaType: "application/json", Data: voice}}
	default:
		kind, media.Role, media.MediaType = otogi.PartFile, otogi.MediaDocument, ""
	}
	return otogi.Part{Kind: kind, Media: media}
}

// filenameMD5 reads an image attachment's file name as the MD5 of its
// content: QQ names images by it, in either case, with an extension that
// need not match the format (a ".jpg" can be a PNG). It returns nil for a
// name that is not 32 hex digits. QQ does not re-encode images, so a
// forwarded or saved and resent picture keeps it. Videos are named the same
// way, but that was never checked against their content.
func filenameMD5(name string) []byte {
	base, _, _ := strings.Cut(name, ".")
	if len(base) != 32 {
		return nil
	}
	sum, err := hex.DecodeString(base)
	if err != nil {
		return nil
	}
	return sum
}

// service maps a dispatch that is not a message: bot added or removed,
// friends, message switches, member changes, interactions, and types this
// driver does not know yet.
func (m *mapper) service(f frame, received time.Time) (otogi.Event, error) {
	var d struct {
		Timestamp      json.RawMessage `json:"timestamp"`
		GroupOpenID    string          `json:"group_openid"`
		OpenID         string          `json:"openid"`
		UserOpenID     string          `json:"user_openid"`
		OpMemberOpenID string          `json:"op_member_openid"`
		MemberOpenID   string          `json:"member_openid"`
		GroupMember    string          `json:"group_member_openid"`
	}
	if err := json.Unmarshal(f.D, &d); err != nil {
		return otogi.Event{}, fmt.Errorf("decode %s: %w", f.T, err)
	}
	e := otogi.Event{
		Platform: Platform, Account: m.account, SourceID: otogi.SourceEventID(f.ID),
		Action: otogi.ActionMessageService, Direction: otogi.DirectionSystem,
		OccurredAt: received, ReceivedAt: received, Completeness: otogi.CompletenessComplete,
		Message: otogi.Message{Roots: []otogi.PartID{"service"}, Parts: []otogi.Part{{ID: "service", Kind: otogi.PartOpaque,
			Opaque: &otogi.Extension{Namespace: extensionNamespace, Name: f.T, Version: 1, MediaType: "application/json", Data: f.D}}}},
	}
	user := firstOf(d.OpMemberOpenID, d.MemberOpenID, d.GroupMember, d.UserOpenID, d.OpenID)
	switch {
	case d.GroupOpenID != "":
		e.Conversation = otogi.Conversation{ID: otogi.ConversationID("group:" + d.GroupOpenID), Kind: otogi.ConversationGroup}
	case user != "":
		e.Conversation = otogi.Conversation{ID: otogi.ConversationID("user:" + user), Kind: otogi.ConversationDirect}
	default:
		// Guild events and anything without a chat: one conversation for the
		// bot as a whole.
		e.Conversation = otogi.Conversation{ID: "bot", Kind: otogi.ConversationChannel}
	}
	if user != "" {
		e.Actor = otogi.Actor{ID: otogi.ActorID("user:" + user)}
	}
	return e, nil
}

// memberRole is a group member's standing as QQ gives it: owner or admin,
// and nothing for an ordinary member or a value it did not give before.
func memberRole(role string) otogi.ActorRole {
	switch role {
	case "owner":
		return otogi.RoleOwner
	case "admin":
		return otogi.RoleAdmin
	}
	return ""
}

func firstOf(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

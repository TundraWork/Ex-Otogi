package qq

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

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
		Bot          bool   `json:"bot"`
	} `json:"author"`
	Mentions []struct {
		ID           string `json:"id"`
		MemberOpenID string `json:"member_openid"`
		UserOpenID   string `json:"user_openid"`
		Username     string `json:"username"`
		IsYou        bool   `json:"is_you"`
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

// sentType is the dispatch type of a C2C message the bot sent, which the
// driver queues itself: QQ does not echo those.
const sentType = "OTOGI_C2C_SENT"

// sentMessage is the payload of a sentType dispatch.
type sentMessage struct {
	ID        string `json:"id"`
	Content   string `json:"content"` // as sent: text, or Markdown
	Timestamp string `json:"timestamp"`
	Peer      string `json:"peer"` // the user's openid
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

// mapper turns dispatches into events.
type mapper struct {
	account otogi.AccountID
	refs    refs
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
		// QQ strips the @ from these itself; a C2C message is always for
		// the bot.
		Mentioned: f.T == "GROUP_AT_MESSAGE_CREATE" || f.T == "C2C_MESSAGE_CREATE",
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
	e.Actor = otogi.Actor{ID: otogi.ActorID("user:" + actor), Name: msg.Author.Username}

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

// sent maps a C2C message the bot sent.
func (m *mapper) sent(f frame, received time.Time) (otogi.Event, error) {
	var s sentMessage
	if err := json.Unmarshal(f.D, &s); err != nil {
		return otogi.Event{}, fmt.Errorf("decode %s: %w", f.T, err)
	}
	e := otogi.Event{
		Platform: Platform, Account: m.account, SourceID: otogi.SourceEventID(s.ID),
		Conversation: otogi.Conversation{ID: otogi.ConversationID("user:" + s.Peer), Kind: otogi.ConversationDirect},
		Actor:        otogi.Actor{ID: otogi.ActorID("user:" + s.Self), Name: s.SelfName},
		Action:       otogi.ActionMessageCreated, Direction: otogi.DirectionOutgoing,
		ReceivedAt: received, Completeness: otogi.CompletenessComplete, Message: otogi.TextMessage(s.Content),
	}
	if t, err := time.Parse(time.RFC3339, s.Timestamp); err == nil {
		e.OccurredAt = t
	}
	return e, nil
}

var mentionToken = regexp.MustCompile(`<@!?([^>\s]+)>`)

// content builds the message: text with mentions as annotations, then the
// attachments, and a reply relation when it quotes another message.
func (m *mapper) content(ctx context.Context, msg message, e *otogi.Event) (otogi.Message, error) {
	var text otogi.RichText
	content := msg.Content
	if e.Mentioned {
		content = strings.TrimLeft(content, " ")
	}
	last := 0
	for _, loc := range mentionToken.FindAllStringSubmatchIndex(content, -1) {
		text.Write(content[last:loc[0]])
		last = loc[1]
		id := content[loc[2]:loc[3]]
		name, target := id, otogi.ActorID("user:"+id)
		for _, mention := range msg.Mentions {
			if mention.MemberOpenID == id || mention.UserOpenID == id || mention.ID == id {
				if mention.Username != "" {
					name = mention.Username
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
	case strings.HasPrefix(a.ContentType, "video/"):
		media.Role = otogi.MediaVideo
	case a.ContentType == "voice" || strings.HasPrefix(a.ContentType, "audio/"):
		media.Role, media.MediaType = otogi.MediaVoice, "audio/silk"
		if a.ContentType != "voice" {
			media.MediaType = a.ContentType
		}
		voice, _ := json.Marshal(map[string]string{"wav_url": a.VoiceWavURL, "asr_text": a.ASRText})
		media.Extensions = []otogi.Extension{{Namespace: extensionNamespace, Name: "voice", Version: 1, MediaType: "application/json", Data: voice}}
	default:
		kind, media.Role, media.MediaType = otogi.PartFile, otogi.MediaDocument, ""
	}
	return otogi.Part{Kind: kind, Media: media}
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

func firstOf(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

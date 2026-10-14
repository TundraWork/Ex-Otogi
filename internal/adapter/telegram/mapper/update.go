package mapper

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gotd/td/tg"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// Config supplies stable protocol-neutral scope and the ingestion clock.
type Config struct {
	// Platform is the validated Telegram platform identity.
	Platform otogi.PlatformID
	// Account is the configured deployment account plus authorized-user identity.
	Account otogi.AccountID
	// IngestedAt supplies the deterministic capture timestamp.
	IngestedAt func() time.Time
}

// Mapper converts Telegram updates into rich events. It has no I/O.
type Mapper struct{ config Config }

// Result is what one update maps to.
type Result struct {
	// Events are the routed events: one for a new or edited message.
	Events []otogi.Event
	// Locators record which conversation each mapped message belongs to.
	Locators []Locator
	// Deletes are deleted messages. A common-box deletion has no Conversation;
	// the caller resolves it from recorded locators and calls MapDeletion.
	Deletes []Delete
}

// Locator records that a message ID in a box belongs to a conversation.
type Locator struct {
	// Box is "common" or "channel:<id>".
	Box string
	// MessageID is Telegram's message ID within Box.
	MessageID int
	// Conversation is the message's conversation.
	Conversation string
}

// Delete is one deleted message.
type Delete struct {
	// Box is "common" or "channel:<id>".
	Box string
	// MessageID is Telegram's message ID within Box.
	MessageID int
	// Conversation is set for channel deletions and empty for common ones.
	Conversation string
}

// New creates a mapper.
func New(config Config) *Mapper { return &Mapper{config: config} }

// MapDeletion builds the deletion event for one message in conversation.
func (m *Mapper) MapDeletion(d Delete, conversation string) otogi.Event {
	namespace := namespaceForBox(d.Box)
	data, _ := EncodeMessageLocator(MessageLocator{Namespace: namespace, Peer: conversation, MessageID: d.MessageID})
	now := m.now()
	return otogi.Event{
		Platform: m.config.Platform, Account: m.account(),
		Conversation: otogi.Conversation{ID: otogi.ConversationID(conversation), Kind: conversationKind(conversation)},
		SourceID:     sourceID(namespace, conversation, d.MessageID),
		Action:       otogi.ActionMessageDeleted, Direction: otogi.DirectionSystem,
		OccurredAt: now, ReceivedAt: now, Completeness: otogi.CompletenessUnavailable,
		Locator: messageLocator(data),
	}
}

// Map maps a routed update (see Routed). Other updates return an error.
func (m *Mapper) Map(update tg.UpdateClass) (Result, error) {
	switch value := update.(type) {
	case *tg.UpdateNewMessage:
		return m.mapMessage(value.Message, otogi.ActionMessageCreated, NamespaceCommon)
	case *tg.UpdateEditMessage:
		return m.mapMessage(value.Message, otogi.ActionMessageEdited, NamespaceCommon)
	case *tg.UpdateNewChannelMessage:
		return m.mapMessage(value.Message, otogi.ActionMessageCreated, NamespaceChannel)
	case *tg.UpdateEditChannelMessage:
		return m.mapMessage(value.Message, otogi.ActionMessageEdited, NamespaceChannel)
	case *tg.UpdateDeleteMessages:
		return mapDeletes(NamespaceCommon, "", value.Messages), nil
	case *tg.UpdateDeleteChannelMessages:
		return mapDeletes("channel:"+strconv.FormatInt(value.ChannelID, 10), "channel:"+strconv.FormatInt(value.ChannelID, 10), value.Messages), nil
	default:
		return Result{}, fmt.Errorf("telegram update %T is not routed: %w", update, otogi.ErrUnsupported)
	}
}

func (m *Mapper) mapMessage(message tg.MessageClass, action otogi.EventAction, namespace string) (Result, error) {
	var peer tg.PeerClass
	var id, date int
	var actor string
	direction := otogi.DirectionSystem
	completeness := otogi.CompletenessComplete
	messageAction := action
	var rich otogi.Message
	var editedAt time.Time

	switch value := message.(type) {
	case *tg.Message:
		peer, id, date = value.PeerID, value.ID, value.Date
		var err error
		direction, actor, err = messageFlow(value)
		if err != nil {
			return Result{}, err
		}
		rich, err = m.mapRegularMessage(value)
		if err != nil {
			return Result{}, err
		}
		if edit, ok := value.GetEditDate(); ok {
			editedAt = time.Unix(int64(edit), 0).UTC()
		}
	case *tg.MessageService:
		peer, id, date = value.PeerID, value.ID, value.Date
		if value.Out {
			direction = otogi.DirectionOutgoing
		} else {
			direction = otogi.DirectionIncoming
		}
		if from, ok := value.GetFromID(); ok {
			actor, _ = PeerID(from)
		}
		messageAction = otogi.ActionMessageService
		var err error
		rich, err = m.mapServiceMessage(value)
		if err != nil {
			return Result{}, err
		}
	case *tg.MessageEmpty:
		id = value.ID
		var ok bool
		peer, ok = value.GetPeerID()
		if !ok {
			return Result{}, fmt.Errorf("empty message without peer: %w", otogi.ErrInvalid)
		}
		completeness = otogi.CompletenessUnavailable
		rich = otogi.Message{}
	default:
		return Result{}, fmt.Errorf("unsupported MessageClass %T: %w", message, otogi.ErrInvalid)
	}
	conversation, err := PeerID(peer)
	if err != nil {
		return Result{}, err
	}
	locator := MessageLocator{Namespace: namespace, Peer: conversation, MessageID: id}
	locatorData, err := EncodeMessageLocator(locator)
	if err != nil {
		return Result{}, err
	}
	received := m.now()
	occurred := received
	if date > 0 {
		occurred = time.Unix(int64(date), 0).UTC()
	}
	event := otogi.Event{
		Platform: m.config.Platform, Account: m.account(),
		Conversation: otogi.Conversation{ID: otogi.ConversationID(conversation), Kind: conversationKind(conversation)},
		SourceID:     sourceID(namespace, conversation, id),
		Actor:        otogi.Actor{ID: otogi.ActorID(actor)},
		Action:       messageAction, Direction: direction,
		OccurredAt: occurred, ReceivedAt: received, EditedAt: editedAt,
		Completeness: completeness, Locator: messageLocator(locatorData), Message: rich,
	}
	box := NamespaceCommon
	if namespace == NamespaceChannel {
		box = conversation
	}
	return Result{Events: []otogi.Event{event}, Locators: []Locator{{Box: box, MessageID: id, Conversation: conversation}}}, nil
}

func (m *Mapper) mapServiceMessage(message *tg.MessageService) (otogi.Message, error) {
	ext, err := telegramExtension("message-service", message)
	if err != nil {
		return otogi.Message{}, err
	}
	return otogi.Message{
		Roots:     []otogi.PartID{"service"},
		Parts:     []otogi.Part{{ID: "service", Kind: otogi.PartOpaque, Opaque: &ext}},
		Relations: m.replyRelations(message.PeerID, message.ReplyTo),
	}, nil
}

func (m *Mapper) mapRegularMessage(message *tg.Message) (otogi.Message, error) {
	var rich otogi.Message
	if message.Message != "" {
		entities, _ := message.GetEntities()
		annotations, unconverted, err := ConvertEntities(message.Message, entities)
		if err != nil {
			return otogi.Message{}, err
		}
		rich.Parts = append(rich.Parts, otogi.Part{ID: "text", Kind: otogi.PartText, Text: message.Message, Annotations: annotations, Extensions: unconverted})
	}
	if media, ok := message.GetMedia(); ok {
		part, err := mapMedia(media)
		if err != nil {
			return otogi.Message{}, err
		}
		rich.Parts = append(rich.Parts, part)
	}
	if len(rich.Parts) == 0 {
		ext, err := telegramExtension("message", message)
		if err != nil {
			return otogi.Message{}, err
		}
		rich.Parts = append(rich.Parts, otogi.Part{ID: "opaque", Kind: otogi.PartOpaque, Opaque: &ext})
	}
	for _, part := range rich.Parts {
		rich.Roots = append(rich.Roots, part.ID)
	}
	metadata := []struct {
		name    string
		present bool
	}{{"reply", message.ReplyTo != nil}, {"forward", message.FwdFrom.FromID != nil || message.FwdFrom.FromName != ""}, {"grouped", message.GroupedID != 0}, {"reactions", message.Reactions.Results != nil}, {"raw", true}}
	for _, item := range metadata {
		if !item.present {
			continue
		}
		ext, err := telegramExtension("message-"+item.name, message)
		if err != nil {
			return otogi.Message{}, err
		}
		rich.Extensions = append(rich.Extensions, ext)
	}
	rich.Relations = m.replyRelations(message.PeerID, message.ReplyTo)
	return rich, nil
}

// replyRelations maps a reply header to a reply relation and, inside forum
// topics, a thread relation to the topic's top message.
func (m *Mapper) replyRelations(peer tg.PeerClass, replyTo tg.MessageReplyHeaderClass) []otogi.Relation {
	header, ok := replyTo.(*tg.MessageReplyHeader)
	if !ok || header.ReplyToMsgID <= 0 {
		return nil
	}
	if explicit, ok := header.GetReplyToPeerID(); ok {
		peer = explicit
	}
	conversation, err := PeerID(peer)
	if err != nil {
		return nil
	}
	namespace := namespaceForPeer(peer)
	relation := func(kind otogi.RelationKind, target int) otogi.Relation {
		data, _ := EncodeMessageLocator(MessageLocator{Namespace: namespace, Peer: conversation, MessageID: target})
		return otogi.Relation{Kind: kind, Target: otogi.EventRef{
			Platform: m.config.Platform, Account: m.account(), Conversation: otogi.ConversationID(conversation),
			SourceEvent: sourceID(namespace, conversation, target), Locator: messageLocator(data),
		}}
	}
	relations := []otogi.Relation{relation(otogi.RelationReply, header.ReplyToMsgID)}
	if top, ok := header.GetReplyToTopID(); ok && top > 0 && top != header.ReplyToMsgID {
		relations = append(relations, relation(otogi.RelationThread, top))
	}
	return relations
}

func namespaceForPeer(peer tg.PeerClass) string {
	if _, ok := peer.(*tg.PeerChannel); ok {
		return NamespaceChannel
	}
	return NamespaceCommon
}

func mapMedia(media tg.MessageMediaClass) (otogi.Part, error) {
	raw, err := telegramExtension("message-media", media)
	if err != nil {
		return otogi.Part{}, err
	}
	part := otogi.Part{ID: "media"}
	switch value := media.(type) {
	case *tg.MessageMediaGeo:
		if point, ok := value.Geo.(*tg.GeoPoint); ok {
			part.Kind, part.Location, part.Extensions = otogi.PartLocation, &otogi.Location{Latitude: point.Lat, Longitude: point.Long}, []otogi.Extension{raw}
			return part, nil
		}
	case *tg.MessageMediaContact:
		name := strings.TrimSpace(value.FirstName + " " + value.LastName)
		part.Kind, part.Contact, part.Extensions = otogi.PartContact, &otogi.Contact{DisplayName: name, Address: value.PhoneNumber}, []otogi.Extension{raw}
		return part, nil
	case *tg.MessageMediaDocument:
		if document, ok := value.Document.(*tg.Document); ok {
			role := otogi.MediaDocument
			switch {
			case value.Video:
				role = otogi.MediaVideo
			case value.Voice:
				role = otogi.MediaVoice
			}
			return mediaPart(part, role, document.MimeType, document.Size, MediaLocator{Kind: "document", ID: document.ID, AccessHash: document.AccessHash, FileReference: document.FileReference, DCID: document.DCID}, raw)
		}
	case *tg.MessageMediaPhoto:
		if photo, ok := value.Photo.(*tg.Photo); ok {
			return mediaPart(part, otogi.MediaImage, "image/*", 0, MediaLocator{Kind: "photo", ID: photo.ID, AccessHash: photo.AccessHash, FileReference: photo.FileReference, DCID: photo.DCID}, raw)
		}
	}
	part.Kind, part.Opaque = otogi.PartOpaque, &raw
	return part, nil
}

func mediaPart(part otogi.Part, role otogi.MediaRole, mediaType string, size int64, locator MediaLocator, raw otogi.Extension) (otogi.Part, error) {
	data, err := EncodeMediaLocator(locator)
	if err != nil {
		return otogi.Part{}, err
	}
	part.Kind = otogi.PartMedia
	part.Media = &otogi.Media{
		Role: role, MediaType: mediaType, ByteSize: size,
		Source:     otogi.MediaSource{Kind: otogi.MediaSourceOpaque, Locator: otogi.ProviderLocator{Namespace: extensionNamespace + "/media", Version: 1, Data: data}},
		Extensions: []otogi.Extension{raw},
	}
	return part, nil
}

func mapDeletes(box, conversation string, ids []int) Result {
	result := Result{Deletes: make([]Delete, 0, len(ids))}
	for _, id := range ids {
		result.Deletes = append(result.Deletes, Delete{Box: box, MessageID: id, Conversation: conversation})
	}
	return result
}

func namespaceForBox(box string) string {
	if strings.HasPrefix(box, "channel:") {
		return NamespaceChannel
	}
	return NamespaceCommon
}

func (m *Mapper) account() otogi.AccountID { return m.config.Account }

func (m *Mapper) now() time.Time {
	if m.config.IngestedAt != nil {
		return m.config.IngestedAt().UTC()
	}
	return time.Now().UTC()
}

func sourceID(namespace, conversation string, messageID int) otogi.SourceEventID {
	return otogi.SourceEventID(fmt.Sprintf("telegram:%s:%s:%d", namespace, conversation, messageID))
}

func messageLocator(data []byte) otogi.ProviderLocator {
	return otogi.ProviderLocator{Namespace: extensionNamespace + "/message", Version: 1, Data: data}
}

// conversationKind classifies a tagged peer ID from PeerID.
func conversationKind(conversation string) otogi.ConversationKind {
	switch {
	case strings.HasPrefix(conversation, "user:"):
		return otogi.ConversationDirect
	case strings.HasPrefix(conversation, "chat:"):
		return otogi.ConversationGroup
	default:
		return otogi.ConversationChannel
	}
}

// MessageRef returns a reference to message messageID in conversation, in the
// same form as inbound events use. A zero messageID gives a reference with only
// the conversation, for sends Telegram confirmed without identifying.
func (m *Mapper) MessageRef(conversation string, messageID int) otogi.EventRef {
	ref := otogi.EventRef{Platform: m.config.Platform, Account: m.account(), Conversation: otogi.ConversationID(conversation)}
	if messageID == 0 {
		return ref
	}
	namespace := NamespaceCommon
	if strings.HasPrefix(conversation, "channel:") {
		namespace = NamespaceChannel
	}
	data, _ := EncodeMessageLocator(MessageLocator{Namespace: namespace, Peer: conversation, MessageID: messageID})
	ref.SourceEvent = sourceID(namespace, conversation, messageID)
	ref.Locator = messageLocator(data)
	return ref
}

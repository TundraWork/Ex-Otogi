package mapper

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// Config supplies stable protocol-neutral scope and the ingestion clock.
type Config struct {
	// Platform is the validated Telegram platform identity.
	Platform otogi.PlatformID
	// Account is the configured deployment account plus authorized-user identity.
	Account otogi.AccountID
	// AuthorizedUserID is the signed-in Telegram user ID.
	AuthorizedUserID int64
	// IngestedAt supplies the deterministic capture timestamp.
	IngestedAt func() time.Time
}

// Mapper deterministically maps layer-228 Telegram values without network access.
type Mapper struct{ config Config }

// LocatorRecord associates a mapped provider message with its durable origin.
type LocatorRecord struct {
	// Namespace identifies the common or channel message ID space.
	Namespace string
	// MessageID is the positive Telegram message ID.
	MessageID int
	// Conversation is the canonical tagged conversation identity.
	Conversation string
	// Payload is the encoded provider locator.
	Payload []byte
}

// DeleteRecord describes a Telegram deletion, retaining unresolved common IDs without inventing a peer.
type DeleteRecord struct {
	// Namespace identifies the common or channel message ID space.
	Namespace string
	// MessageID is the deleted Telegram message ID.
	MessageID int
	// Conversation is empty only when a common delete remains unresolved.
	Conversation string
	// Unresolved reports that locator lookup is required before routing.
	Unresolved bool
	// SourceKey is the stable deletion deduplication key.
	SourceKey string
}

// Result is one deterministic mapping outcome.
type Result struct {
	// Event is the mapped rich event when HasEvent is true.
	Event otogi.Event
	// HasEvent reports whether Event contains a routed update.
	HasEvent bool
	// SourceKey is the stable provider deduplication key.
	SourceKey string
	// Box identifies the common or channel consistency stream.
	Box string
	// Position is the nonnegative Telegram pts position when available.
	Position int
	// Locators are provider identities learned while mapping.
	Locators []LocatorRecord
	// Deletes contains one item for every deleted message ID.
	Deletes []DeleteRecord
	// Quarantine contains a non-routed update preservation record.
	Quarantine *QuarantineRecord
}

// New creates a mapper with immutable scope configuration.
func New(config Config) *Mapper { return &Mapper{config: config} }

// MapDeletion builds the deletion event for one message once its conversation is known.
func (m *Mapper) MapDeletion(deletion DeleteRecord, conversation string) (otogi.Event, error) {
	if deletion.MessageID <= 0 || conversation == "" {
		return otogi.Event{}, fmt.Errorf("invalid Telegram deletion: %w", otogi.ErrInvalid)
	}
	locatorData, err := EncodeMessageLocator(MessageLocator{Namespace: deletion.Namespace, Peer: conversation, MessageID: deletion.MessageID})
	if err != nil {
		return otogi.Event{}, err
	}
	now := m.now()
	return otogi.Event{
		Platform: m.config.Platform, Account: m.account(),
		Conversation: otogi.Conversation{ID: otogi.ConversationID(conversation), Kind: conversationKind(conversation)},
		SourceID:     sourceID(deletion.Namespace, conversation, deletion.MessageID),
		Action:       otogi.ActionMessageDeleted, Direction: otogi.DirectionSystem,
		OccurredAt: now, ReceivedAt: now, Completeness: otogi.CompletenessUnavailable,
		Locator: messageLocator(locatorData),
	}, nil
}

// Map maps a supported update or returns a raw quarantine record for a non-routed update.
func (m *Mapper) Map(update tg.UpdateClass) (Result, error) {
	switch value := update.(type) {
	case *tg.UpdateNewMessage:
		return m.mapMessage(value.Message, otogi.ActionMessageCreated, NamespaceCommon, value.Pts)
	case *tg.UpdateEditMessage:
		return m.mapMessage(value.Message, otogi.ActionMessageEdited, NamespaceCommon, value.Pts)
	case *tg.UpdateNewChannelMessage:
		return m.mapMessage(value.Message, otogi.ActionMessageCreated, NamespaceChannel, value.Pts)
	case *tg.UpdateEditChannelMessage:
		return m.mapMessage(value.Message, otogi.ActionMessageEdited, NamespaceChannel, value.Pts)
	case *tg.UpdateDeleteMessages:
		return m.mapDeletes(NamespaceCommon, 0, value.Messages, value.Pts)
	case *tg.UpdateDeleteChannelMessages:
		return m.mapDeletes(NamespaceChannel, value.ChannelID, value.Messages, value.Pts)
	default:
		record, err := Quarantine(update, m.accountScope(), 0)
		if err != nil {
			return Result{}, err
		}
		return Result{Quarantine: &record}, nil
	}
}

func (m *Mapper) mapMessage(message tg.MessageClass, action otogi.EventAction, namespace string, position int) (Result, error) {
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
	sourceKey, err := stableSourceKey(m.accountScope(), locator, messageAction, position, message)
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
	return Result{Event: event, HasEvent: true, SourceKey: sourceKey, Box: box, Position: normalizedPosition(position), Locators: []LocatorRecord{{Namespace: namespace, MessageID: id, Conversation: conversation, Payload: locatorData}}}, nil
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

func (m *Mapper) mapDeletes(namespace string, channelID int64, ids []int, position int) (Result, error) {
	result := Result{Box: NamespaceCommon, Position: normalizedPosition(position), Deletes: make([]DeleteRecord, 0, len(ids))}
	conversation := ""
	if namespace == NamespaceChannel {
		conversation = fmt.Sprintf("channel:%d", channelID)
		result.Box = conversation
	}
	for _, id := range ids {
		sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%d", m.accountScope(), namespace, conversation, id, normalizedPosition(position))))
		result.Deletes = append(result.Deletes, DeleteRecord{Namespace: namespace, MessageID: id, Conversation: conversation, Unresolved: conversation == "", SourceKey: fmt.Sprintf("telegram:delete:%x", sum[:])})
	}
	return result, nil
}

func stableSourceKey(account string, locator MessageLocator, action otogi.EventAction, position int, message tg.MessageClass) (string, error) {
	var raw bin.Buffer
	if err := message.Encode(&raw); err != nil {
		return "", err
	}
	sum := sha256.Sum256(append([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%s\x00%d\x00", account, locator.Namespace, locator.Peer, locator.MessageID, action, normalizedPosition(position))), raw.Buf...))
	return fmt.Sprintf("telegram:%x", sum[:]), nil
}

func normalizedPosition(position int) int {
	if position < 0 {
		return 0
	}
	return position
}

func (m *Mapper) accountScope() string {
	return fmt.Sprintf("%s:user:%d", m.config.Account, m.config.AuthorizedUserID)
}

func (m *Mapper) account() otogi.AccountID { return otogi.AccountID(m.accountScope()) }

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

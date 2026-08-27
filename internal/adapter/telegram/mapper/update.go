package mapper

import (
	"crypto/sha256"
	"fmt"
	"reflect"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// MaxDeleteMessages bounds one Telegram deletion update before allocation.
const MaxDeleteMessages = 10_000

// Config supplies stable protocol-neutral scope and the ingestion clock.
type Config struct {
	// Platform is the validated Telegram platform identity.
	Platform otogi.PlatformID
	// Account is the configured deployment account plus authorized-user identity.
	Account otogi.AccountID
	// Tenant is the protocol-neutral isolation tenant.
	Tenant otogi.TenantID
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
	Event otogi.EventInput
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

// Map maps a supported update or returns a raw quarantine record for a non-routed update.
func (m *Mapper) Map(update tg.UpdateClass) (Result, error) {
	if m.config.AuthorizedUserID <= 0 {
		return Result{}, fmt.Errorf("invalid authorized Telegram user: %w", otogi.ErrInvalid)
	}
	if update == nil || reflect.ValueOf(update).Kind() == reflect.Pointer && reflect.ValueOf(update).IsNil() {
		return Result{}, fmt.Errorf("nil Telegram update: %w", otogi.ErrInvalid)
	}
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
	if message == nil || reflect.ValueOf(message).Kind() == reflect.Pointer && reflect.ValueOf(message).IsNil() {
		return Result{}, fmt.Errorf("nil Telegram message: %w", otogi.ErrInvalid)
	}
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
	providerLocator, err := otogi.NewProviderLocator(extensionNamespace+"/message", 1, locatorData)
	if err != nil {
		return Result{}, err
	}
	sourceKey, err := stableSourceKey(m.accountScope(), locator, messageAction, position, message)
	if err != nil {
		return Result{}, err
	}
	sourceID, err := otogi.NewSourceEventID(fmt.Sprintf("telegram:%s:%s:%d", namespace, conversation, id))
	if err != nil {
		return Result{}, err
	}
	conversationID, err := otogi.NewConversationID(conversation)
	if err != nil {
		return Result{}, err
	}
	var actorID otogi.ActorID
	if actor != "" {
		actorID, err = otogi.NewActorID(actor)
		if err != nil {
			return Result{}, err
		}
	}
	ingested := time.Now().UTC()
	if m.config.IngestedAt != nil {
		ingested = m.config.IngestedAt().UTC()
	}
	occurred := ingested
	if date > 0 {
		occurred = time.Unix(int64(date), 0).UTC()
	}
	accountID, err := otogi.NewAccountID(m.accountScope())
	if err != nil {
		return Result{}, err
	}
	input, err := otogi.NewEventInput(otogi.EventInputParams{Platform: m.config.Platform, Account: accountID, Tenant: m.config.Tenant, Conversation: conversationID, SourceEventID: sourceID, Actor: actorID, Action: messageAction, Direction: direction, OccurredAt: occurred, IngestedAt: ingested, EditedAt: editedAt, Completeness: completeness, ProviderLocator: providerLocator, Message: rich})
	if err != nil {
		return Result{}, err
	}
	box := NamespaceCommon
	if namespace == NamespaceChannel {
		box = conversation
	}
	return Result{Event: input, HasEvent: true, SourceKey: sourceKey, Box: box, Position: normalizedPosition(position), Locators: []LocatorRecord{{Namespace: namespace, MessageID: id, Conversation: conversation, Payload: locatorData}}}, nil
}

func (m *Mapper) mapServiceMessage(message *tg.MessageService) (otogi.Message, error) {
	ext, err := telegramExtension("message-service", message)
	if err != nil {
		return otogi.Message{}, err
	}
	id, _ := otogi.NewPartID("service")
	part, err := otogi.NewOpaquePart(id, ext, nil, nil)
	if err != nil {
		return otogi.Message{}, err
	}
	relations := make([]otogi.Relation, 0, 2)
	if header, ok := message.ReplyTo.(*tg.MessageReplyHeader); ok && header.ReplyToMsgID > 0 {
		targetPeer := message.PeerID
		if explicit, ok := header.GetReplyToPeerID(); ok {
			targetPeer = explicit
		}
		conversation, err := PeerID(targetPeer)
		if err != nil {
			return otogi.Message{}, err
		}
		makeRelation := func(kind otogi.RelationKind, targetID int) (otogi.Relation, error) {
			conversationID, err := otogi.NewConversationID(conversation)
			if err != nil {
				return otogi.Relation{}, err
			}
			namespace := namespaceForPeer(targetPeer)
			sourceID, err := otogi.NewSourceEventID(fmt.Sprintf("telegram:%s:%s:%d", namespace, conversation, targetID))
			if err != nil {
				return otogi.Relation{}, err
			}
			locatorData, err := EncodeMessageLocator(MessageLocator{Namespace: namespace, Peer: conversation, MessageID: targetID})
			if err != nil {
				return otogi.Relation{}, err
			}
			locator, err := otogi.NewProviderLocator(extensionNamespace+"/message", 1, locatorData)
			if err != nil {
				return otogi.Relation{}, err
			}
			accountID, err := otogi.NewAccountID(m.accountScope())
			if err != nil {
				return otogi.Relation{}, err
			}
			ref, err := otogi.NewEventRef(otogi.EventRefParams{Platform: m.config.Platform, Account: accountID, Conversation: conversationID, SourceEvent: sourceID, Locator: locator})
			if err != nil {
				return otogi.Relation{}, err
			}
			return otogi.NewRelation(kind, ref, "", nil)
		}
		reply, err := makeRelation(otogi.RelationReply, header.ReplyToMsgID)
		if err != nil {
			return otogi.Message{}, err
		}
		relations = append(relations, reply)
		if topID, ok := header.GetReplyToTopID(); ok && topID > 0 && topID != header.ReplyToMsgID {
			thread, err := makeRelation(otogi.RelationThread, topID)
			if err != nil {
				return otogi.Message{}, err
			}
			relations = append(relations, thread)
		}
	}
	return otogi.NewMessage([]otogi.PartID{id}, []otogi.Part{part}, relations, nil)
}

func (m *Mapper) mapRegularMessage(message *tg.Message) (otogi.Message, error) {
	parts := make([]otogi.Part, 0, 2)
	roots := make([]otogi.PartID, 0, 2)
	messageExtensions := make([]otogi.Extension, 0, 4)
	if message.Message != "" {
		entities, _ := message.GetEntities()
		annotations, malformed, err := ConvertEntities(message.Message, entities)
		if err != nil {
			return otogi.Message{}, err
		}
		id, _ := otogi.NewPartID("text")
		part, err := otogi.NewTextPart(id, message.Message, annotations, nil, malformed)
		if err != nil {
			return otogi.Message{}, err
		}
		parts = append(parts, part)
		roots = append(roots, id)
	}
	if media, ok := message.GetMedia(); ok {
		part, id, err := mapMedia(media)
		if err != nil {
			return otogi.Message{}, err
		}
		parts = append(parts, part)
		roots = append(roots, id)
	}
	relations := make([]otogi.Relation, 0, 2)
	metadata := []struct {
		name    string
		present bool
	}{{"reply", message.ReplyTo != nil}, {"forward", message.FwdFrom.FromID != nil || message.FwdFrom.FromName != ""}, {"grouped", message.GroupedID != 0}, {"reactions", message.Reactions.Results != nil}}
	for _, item := range metadata {
		if !item.present {
			continue
		}
		ext, err := telegramExtension("message-"+item.name, message)
		if err != nil {
			return otogi.Message{}, err
		}
		messageExtensions = append(messageExtensions, ext)
	}
	rawMessage, err := telegramExtension("message-raw", message)
	if err != nil {
		return otogi.Message{}, err
	}
	messageExtensions = append(messageExtensions, rawMessage)
	if header, ok := message.ReplyTo.(*tg.MessageReplyHeader); ok && header.ReplyToMsgID > 0 {
		targetPeer := message.PeerID
		if explicit, ok := header.GetReplyToPeerID(); ok {
			targetPeer = explicit
		}
		conversation, err := PeerID(targetPeer)
		if err != nil {
			return otogi.Message{}, err
		}
		makeRelation := func(kind otogi.RelationKind, targetID int) (otogi.Relation, error) {
			conversationID, err := otogi.NewConversationID(conversation)
			if err != nil {
				return otogi.Relation{}, err
			}
			namespace := namespaceForPeer(targetPeer)
			sourceID, err := otogi.NewSourceEventID(fmt.Sprintf("telegram:%s:%s:%d", namespace, conversation, targetID))
			if err != nil {
				return otogi.Relation{}, err
			}
			locatorData, err := EncodeMessageLocator(MessageLocator{Namespace: namespace, Peer: conversation, MessageID: targetID})
			if err != nil {
				return otogi.Relation{}, err
			}
			locator, err := otogi.NewProviderLocator(extensionNamespace+"/message", 1, locatorData)
			if err != nil {
				return otogi.Relation{}, err
			}
			accountID, err := otogi.NewAccountID(m.accountScope())
			if err != nil {
				return otogi.Relation{}, err
			}
			ref, err := otogi.NewEventRef(otogi.EventRefParams{Platform: m.config.Platform, Account: accountID, Conversation: conversationID, SourceEvent: sourceID, Locator: locator})
			if err != nil {
				return otogi.Relation{}, err
			}
			return otogi.NewRelation(kind, ref, "", nil)
		}
		reply, err := makeRelation(otogi.RelationReply, header.ReplyToMsgID)
		if err != nil {
			return otogi.Message{}, err
		}
		relations = append(relations, reply)
		if topID, ok := header.GetReplyToTopID(); ok && topID > 0 && topID != header.ReplyToMsgID {
			thread, err := makeRelation(otogi.RelationThread, topID)
			if err != nil {
				return otogi.Message{}, err
			}
			relations = append(relations, thread)
		}
	}
	if len(parts) == 0 {
		ext, err := telegramExtension("message", message)
		if err != nil {
			return otogi.Message{}, err
		}
		id, _ := otogi.NewPartID("opaque")
		part, err := otogi.NewOpaquePart(id, ext, nil, nil)
		if err != nil {
			return otogi.Message{}, err
		}
		parts, roots = append(parts, part), append(roots, id)
	}
	return otogi.NewMessage(roots, parts, relations, messageExtensions)
}

func namespaceForPeer(peer tg.PeerClass) string {
	if _, ok := peer.(*tg.PeerChannel); ok {
		return NamespaceChannel
	}
	return NamespaceCommon
}
func mapMedia(media tg.MessageMediaClass) (otogi.Part, otogi.PartID, error) {
	ext, err := telegramExtension("message-media", media)
	if err != nil {
		return otogi.Part{}, otogi.PartID{}, err
	}
	id, _ := otogi.NewPartID("media")
	switch value := media.(type) {
	case *tg.MessageMediaGeo:
		point, ok := value.Geo.(*tg.GeoPoint)
		if ok {
			location, err := otogi.NewLocation(otogi.LocationParams{Latitude: point.Lat, Longitude: point.Long})
			if err != nil {
				return otogi.Part{}, otogi.PartID{}, err
			}
			part, err := otogi.NewLocationPart(id, location, nil, []otogi.Extension{ext})
			return part, id, err
		}
	case *tg.MessageMediaContact:
		displayName := value.FirstName
		if value.LastName != "" {
			displayName += " " + value.LastName
		}
		contact, err := otogi.NewContact(otogi.ContactParams{DisplayName: displayName, Address: value.PhoneNumber})
		if err != nil {
			return otogi.Part{}, otogi.PartID{}, err
		}
		part, err := otogi.NewContactPart(id, contact, nil, []otogi.Extension{ext})
		return part, id, err
	case *tg.MessageMediaDocument:
		if document, ok := value.Document.(*tg.Document); ok {
			role := otogi.MediaDocument
			if value.Video {
				role = otogi.MediaVideo
			} else if value.Voice {
				role = otogi.MediaVoice
			}
			return projectedMediaPart(id, role, document.MimeType, document.Size, MediaLocator{Kind: "document", ID: document.ID, AccessHash: document.AccessHash, FileReference: document.FileReference, DCID: document.DCID}, ext)
		}
	case *tg.MessageMediaPhoto:
		if photo, ok := value.Photo.(*tg.Photo); ok {
			return projectedMediaPart(id, otogi.MediaImage, "image/*", 0, MediaLocator{Kind: "photo", ID: photo.ID, AccessHash: photo.AccessHash, FileReference: photo.FileReference, DCID: photo.DCID}, ext)
		}
	}
	part, err := otogi.NewOpaquePart(id, ext, nil, nil)
	return part, id, err
}

func projectedMediaPart(id otogi.PartID, role otogi.MediaRole, mediaType string, size int64, locator MediaLocator, raw otogi.Extension) (otogi.Part, otogi.PartID, error) {
	data, err := EncodeMediaLocator(locator)
	if err != nil {
		return otogi.Part{}, otogi.PartID{}, err
	}
	provider, err := otogi.NewProviderLocator(extensionNamespace+"/media", 1, data)
	if err != nil {
		return otogi.Part{}, otogi.PartID{}, err
	}
	source, err := otogi.NewOpaqueMediaSource(provider, nil)
	if err != nil {
		return otogi.Part{}, otogi.PartID{}, err
	}
	mapped, err := otogi.NewMedia(otogi.MediaParams{Role: role, MediaType: mediaType, ByteSize: size, Source: source, Extensions: []otogi.Extension{raw}})
	if err != nil {
		return otogi.Part{}, otogi.PartID{}, err
	}
	part, err := otogi.NewMediaPart(id, otogi.PartMedia, mapped, nil, nil)
	return part, id, err
}

func (m *Mapper) mapDeletes(namespace string, channelID int64, ids []int, position int) (Result, error) {
	if len(ids) > MaxDeleteMessages || namespace == NamespaceChannel && channelID <= 0 {
		return Result{}, fmt.Errorf("invalid Telegram deletion update: %w", otogi.ErrCapacity)
	}
	result := Result{Box: NamespaceCommon, Position: normalizedPosition(position), Deletes: make([]DeleteRecord, 0, len(ids))}
	conversation := ""
	if namespace == NamespaceChannel {
		conversation = fmt.Sprintf("channel:%d", channelID)
		result.Box = conversation
	}
	for _, id := range ids {
		if id <= 0 {
			return Result{}, fmt.Errorf("invalid deleted message ID: %w", otogi.ErrInvalid)
		}
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
	return fmt.Sprintf("%s:user:%d", m.config.Account.String(), m.config.AuthorizedUserID)
}

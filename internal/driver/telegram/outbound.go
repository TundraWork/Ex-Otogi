package telegram

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"ex-otogi/pkg/otogi"

	"github.com/gotd/td/crypto"
	gotdtelegram "github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/message"
	"github.com/gotd/td/telegram/message/unpack"
	"github.com/gotd/td/tg"
)

const defaultOutboundTimeout = 3 * time.Second

// OutboundOption mutates outbound dispatcher configuration.
type OutboundOption func(*outboundConfig)

// WithOutboundTimeout configures a timeout bound for each outbound RPC call.
func WithOutboundTimeout(timeout time.Duration) OutboundOption {
	return func(cfg *outboundConfig) {
		if timeout > 0 {
			cfg.rpcTimeout = timeout
		}
	}
}

// WithOutboundLogger configures structured logging for outbound operations.
func WithOutboundLogger(logger *slog.Logger) OutboundOption {
	return func(cfg *outboundConfig) {
		cfg.logger = logger
	}
}

// WithSinkRef configures the sink identity returned by sink-list operations.
func WithSinkRef(ref otogi.EventSink) OutboundOption {
	return func(cfg *outboundConfig) {
		cfg.sink = ref
		if cfg.sink.Platform == "" {
			cfg.sink.Platform = DriverPlatform
		}
	}
}

// SinkDispatcher adapts neutral outbound operations to Telegram RPC calls.
type SinkDispatcher struct {
	cfg      outboundConfig
	peers    *PeerCache
	telegram outboundRPC
}

type outboundConfig struct {
	rpcTimeout time.Duration
	logger     *slog.Logger
	sink       otogi.EventSink
}

// NewOutboundDispatcher creates a Telegram outbound dispatcher using gotd client APIs.
func NewOutboundDispatcher(
	client *gotdtelegram.Client,
	peers *PeerCache,
	options ...OutboundOption,
) (*SinkDispatcher, error) {
	if client == nil {
		return nil, fmt.Errorf("new telegram outbound dispatcher: nil client")
	}

	return newOutboundDispatcherWithRPC(newGotdOutboundRPC(client), peers, options...)
}

func newOutboundDispatcherWithRPC(
	rpc outboundRPC,
	peers *PeerCache,
	options ...OutboundOption,
) (*SinkDispatcher, error) {
	if rpc == nil {
		return nil, fmt.Errorf("new telegram outbound dispatcher: nil rpc adapter")
	}
	if peers == nil {
		return nil, fmt.Errorf("new telegram outbound dispatcher: nil peer cache")
	}

	cfg := outboundConfig{
		rpcTimeout: defaultOutboundTimeout,
		sink: otogi.EventSink{
			Platform: DriverPlatform,
		},
	}
	for _, option := range options {
		option(&cfg)
	}

	return &SinkDispatcher{
		cfg:      cfg,
		peers:    peers,
		telegram: rpc,
	}, nil
}

// SendMessage publishes a text message to a Telegram conversation.
func (d *SinkDispatcher) SendMessage(
	ctx context.Context,
	request otogi.SendMessageRequest,
) (*otogi.OutboundMessage, error) {
	if err := request.Validate(); err != nil {
		return nil, fmt.Errorf("send message validate: %w", err)
	}

	peer, err := d.resolvePeer(request.Target)
	if err != nil {
		return nil, fmt.Errorf("send message resolve peer: %w", err)
	}

	rpcCtx, cancel := d.withTimeout(ctx)
	defer cancel()

	id, err := d.telegram.SendText(rpcCtx, peer, request)
	if err != nil {
		return nil, fmt.Errorf("send message to %s: %w", request.Target.Conversation.ID, err)
	}

	d.logOutbound(
		ctx,
		"send_message",
		"conversation", request.Target.Conversation.ID,
		"conversation_type", request.Target.Conversation.Type,
		"message_id", id,
		"reply_to_message_id", request.ReplyToMessageID,
	)

	return &otogi.OutboundMessage{
		ID:     strconv.Itoa(id),
		Target: request.Target,
	}, nil
}

// EditMessage updates text for an existing Telegram message.
func (d *SinkDispatcher) EditMessage(ctx context.Context, request otogi.EditMessageRequest) error {
	if err := request.Validate(); err != nil {
		return fmt.Errorf("edit message validate: %w", err)
	}

	peer, err := d.resolvePeer(request.Target)
	if err != nil {
		return fmt.Errorf("edit message resolve peer: %w", err)
	}

	messageID, err := parseMessageID(request.MessageID)
	if err != nil {
		return fmt.Errorf("edit message parse id %s: %w", request.MessageID, err)
	}

	rpcCtx, cancel := d.withTimeout(ctx)
	defer cancel()

	if err := d.telegram.EditText(rpcCtx, peer, messageID, request); err != nil {
		return fmt.Errorf("edit message %s: %w", request.MessageID, err)
	}

	d.logOutbound(
		ctx,
		"edit_message",
		"conversation", request.Target.Conversation.ID,
		"conversation_type", request.Target.Conversation.Type,
		"message_id", request.MessageID,
	)

	return nil
}

// DeleteMessage removes an existing Telegram message.
func (d *SinkDispatcher) DeleteMessage(ctx context.Context, request otogi.DeleteMessageRequest) error {
	if err := request.Validate(); err != nil {
		return fmt.Errorf("delete message validate: %w", err)
	}

	peer, err := d.resolvePeer(request.Target)
	if err != nil {
		return fmt.Errorf("delete message resolve peer: %w", err)
	}

	messageID, err := parseMessageID(request.MessageID)
	if err != nil {
		return fmt.Errorf("delete message parse id %s: %w", request.MessageID, err)
	}

	rpcCtx, cancel := d.withTimeout(ctx)
	defer cancel()

	if err := d.telegram.DeleteMessage(rpcCtx, peer, messageID, request.Revoke); err != nil {
		return fmt.Errorf("delete message %s: %w", request.MessageID, err)
	}

	d.logOutbound(
		ctx,
		"delete_message",
		"conversation", request.Target.Conversation.ID,
		"conversation_type", request.Target.Conversation.Type,
		"message_id", request.MessageID,
		"revoke", request.Revoke,
	)

	return nil
}

// SetReaction applies add/remove reaction behavior on an existing Telegram message.
func (d *SinkDispatcher) SetReaction(ctx context.Context, request otogi.SetReactionRequest) error {
	if err := request.Validate(); err != nil {
		return fmt.Errorf("set reaction validate: %w", err)
	}

	peer, err := d.resolvePeer(request.Target)
	if err != nil {
		return fmt.Errorf("set reaction resolve peer: %w", err)
	}

	messageID, err := parseMessageID(request.MessageID)
	if err != nil {
		return fmt.Errorf("set reaction parse id %s: %w", request.MessageID, err)
	}

	var reactions []tg.ReactionClass
	if request.Action == otogi.ReactionActionAdd {
		reaction, err := parseReaction(request.Emoji)
		if err != nil {
			return fmt.Errorf("set reaction parse emoji %s: %w", request.Emoji, err)
		}
		reactions = []tg.ReactionClass{reaction}
	}

	rpcCtx, cancel := d.withTimeout(ctx)
	defer cancel()

	if err := d.telegram.SetReaction(rpcCtx, peer, messageID, reactions); err != nil {
		return fmt.Errorf("set reaction on message %s: %w", request.MessageID, err)
	}

	d.logOutbound(
		ctx,
		"set_reaction",
		"conversation", request.Target.Conversation.ID,
		"conversation_type", request.Target.Conversation.Type,
		"message_id", request.MessageID,
		"action", request.Action,
		"emoji", request.Emoji,
	)

	return nil
}

// ListSinks returns the configured Telegram sink identity.
func (d *SinkDispatcher) ListSinks(ctx context.Context) ([]otogi.EventSink, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("list sinks: %w", err)
	}

	return []otogi.EventSink{d.cfg.sink}, nil
}

// ListSinksByPlatform returns the configured sink when platform matches Telegram.
func (d *SinkDispatcher) ListSinksByPlatform(
	ctx context.Context,
	platform otogi.Platform,
) ([]otogi.EventSink, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("list sinks by platform: %w", err)
	}
	if platform != d.cfg.sink.Platform {
		return []otogi.EventSink{}, nil
	}

	return []otogi.EventSink{d.cfg.sink}, nil
}

func (d *SinkDispatcher) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if d.cfg.rpcTimeout <= 0 {
		return ctx, func() {}
	}

	return context.WithTimeout(ctx, d.cfg.rpcTimeout)
}

func (d *SinkDispatcher) resolvePeer(target otogi.OutboundTarget) (tg.InputPeerClass, error) {
	if target.Sink != nil && target.Sink.Platform != "" && target.Sink.Platform != otogi.PlatformTelegram {
		return nil, fmt.Errorf("%w: platform %s", otogi.ErrOutboundUnsupported, target.Sink.Platform)
	}

	peer, err := d.peers.Resolve(target.Conversation)
	if err != nil {
		return nil, fmt.Errorf("resolve conversation %s: %w", target.Conversation.ID, err)
	}

	return peer, nil
}

func (d *SinkDispatcher) logOutbound(ctx context.Context, operation string, attrs ...any) {
	if d.cfg.logger == nil {
		return
	}

	values := make([]any, 0, 2+len(attrs))
	values = append(values, "operation", operation, "platform", otogi.PlatformTelegram)
	values = append(values, attrs...)
	d.cfg.logger.InfoContext(ctx, "telegram outbound operation", values...)
}

func parseMessageID(raw string) (int, error) {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("%w: invalid message id: %w", otogi.ErrInvalidOutboundRequest, err)
	}
	if value <= 0 {
		return 0, fmt.Errorf("%w: invalid message id", otogi.ErrInvalidOutboundRequest)
	}

	return value, nil
}

func parseReaction(emoji string) (tg.ReactionClass, error) {
	trimmed := strings.TrimSpace(emoji)
	if trimmed == "" {
		return nil, fmt.Errorf("%w: empty emoji", otogi.ErrInvalidOutboundRequest)
	}
	if trimmed == "paid" {
		return &tg.ReactionPaid{}, nil
	}
	if documentID, ok := strings.CutPrefix(trimmed, "custom:"); ok {
		id, err := strconv.ParseInt(documentID, 10, 64)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("%w: invalid custom reaction id", otogi.ErrInvalidOutboundRequest)
		}

		return &tg.ReactionCustomEmoji{DocumentID: id}, nil
	}

	return &tg.ReactionEmoji{Emoticon: trimmed}, nil
}

func mapOutboundTextEntities(text string, entities []otogi.TextEntity) ([]tg.MessageEntityClass, error) {
	if len(entities) == 0 {
		return nil, nil
	}

	utf16Offsets := buildUTF16Offsets(text)
	converted := make([]tg.MessageEntityClass, 0, len(entities))
	for index, entity := range entities {
		start := entity.Offset
		end := entity.Offset + entity.Length
		if start < 0 || end < start || end >= len(utf16Offsets) {
			return nil, fmt.Errorf(
				"entity[%d] invalid range [%d,%d) for text runes %d",
				index,
				start,
				end,
				len(utf16Offsets)-1,
			)
		}

		offsetUTF16 := utf16Offsets[start]
		lengthUTF16 := utf16Offsets[end] - utf16Offsets[start]
		telegramEntity, err := convertOutboundTextEntity(entity, offsetUTF16, lengthUTF16)
		if err != nil {
			return nil, fmt.Errorf("entity[%d] convert: %w", index, err)
		}
		converted = append(converted, telegramEntity)
	}

	return converted, nil
}

func convertOutboundTextEntity(
	entity otogi.TextEntity,
	offset int,
	length int,
) (tg.MessageEntityClass, error) {
	switch normalizeEntityType(entity.Type) {
	case otogi.TextEntityTypeUnknown:
		return &tg.MessageEntityUnknown{Offset: offset, Length: length}, nil
	case otogi.TextEntityTypeMention:
		return &tg.MessageEntityMention{Offset: offset, Length: length}, nil
	case otogi.TextEntityTypeHashtag:
		return &tg.MessageEntityHashtag{Offset: offset, Length: length}, nil
	case otogi.TextEntityTypeBotCommand:
		return &tg.MessageEntityBotCommand{Offset: offset, Length: length}, nil
	case otogi.TextEntityTypeURL:
		return &tg.MessageEntityURL{Offset: offset, Length: length}, nil
	case otogi.TextEntityTypeEmail:
		return &tg.MessageEntityEmail{Offset: offset, Length: length}, nil
	case otogi.TextEntityTypeBold:
		return &tg.MessageEntityBold{Offset: offset, Length: length}, nil
	case otogi.TextEntityTypeItalic:
		return &tg.MessageEntityItalic{Offset: offset, Length: length}, nil
	case otogi.TextEntityTypeCode:
		return &tg.MessageEntityCode{Offset: offset, Length: length}, nil
	case otogi.TextEntityTypePre:
		return &tg.MessageEntityPre{
			Offset:   offset,
			Length:   length,
			Language: entity.Language,
		}, nil
	case otogi.TextEntityTypeTextURL:
		return &tg.MessageEntityTextURL{
			Offset: offset,
			Length: length,
			URL:    entity.URL,
		}, nil
	case otogi.TextEntityTypeMentionName:
		return nil, fmt.Errorf(
			"%w: text entity type %q requires resolved input user and is not supported",
			otogi.ErrOutboundUnsupported,
			entity.Type,
		)
	case otogi.TextEntityTypePhone:
		return &tg.MessageEntityPhone{Offset: offset, Length: length}, nil
	case otogi.TextEntityTypeCashtag:
		return &tg.MessageEntityCashtag{Offset: offset, Length: length}, nil
	case otogi.TextEntityTypeBankCard:
		return &tg.MessageEntityBankCard{Offset: offset, Length: length}, nil
	case otogi.TextEntityTypeUnderline:
		return &tg.MessageEntityUnderline{Offset: offset, Length: length}, nil
	case otogi.TextEntityTypeStrike:
		return &tg.MessageEntityStrike{Offset: offset, Length: length}, nil
	case otogi.TextEntityTypeBlockquote:
		return &tg.MessageEntityBlockquote{
			Collapsed: entity.Collapsed,
			Offset:    offset,
			Length:    length,
		}, nil
	case otogi.TextEntityTypeSpoiler:
		return &tg.MessageEntitySpoiler{Offset: offset, Length: length}, nil
	case otogi.TextEntityTypeCustomEmoji:
		documentID, err := parseEntityID(entity.CustomEmojiID)
		if err != nil {
			return nil, fmt.Errorf("custom_emoji parse custom_emoji_id: %w", err)
		}
		return &tg.MessageEntityCustomEmoji{
			Offset:     offset,
			Length:     length,
			DocumentID: documentID,
		}, nil
	default:
		return nil, fmt.Errorf("%w: unsupported text entity type %q", otogi.ErrOutboundUnsupported, entity.Type)
	}
}

func buildUTF16Offsets(text string) []int {
	offsets := make([]int, 1, len(text)+1)
	current := 0
	for _, value := range text {
		current += utf16RuneLength(value)
		offsets = append(offsets, current)
	}

	return offsets
}

func utf16RuneLength(value rune) int {
	if value >= 0x10000 && value <= 0x10FFFF {
		return 2
	}

	return 1
}

func parseEntityID(raw string) (int64, error) {
	parsed, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%w: invalid entity id %q", otogi.ErrInvalidOutboundRequest, raw)
	}

	return parsed, nil
}

func normalizeEntityType(value otogi.TextEntityType) otogi.TextEntityType {
	switch value {
	case "botcommand":
		return otogi.TextEntityTypeBotCommand
	case "texturl":
		return otogi.TextEntityTypeTextURL
	case "mentionname":
		return otogi.TextEntityTypeMentionName
	case "bankcard":
		return otogi.TextEntityTypeBankCard
	case "customemoji":
		return otogi.TextEntityTypeCustomEmoji
	default:
		return value
	}
}

type outboundRPC interface {
	SendText(ctx context.Context, peer tg.InputPeerClass, request otogi.SendMessageRequest) (int, error)
	EditText(ctx context.Context, peer tg.InputPeerClass, messageID int, request otogi.EditMessageRequest) error
	DeleteMessage(ctx context.Context, peer tg.InputPeerClass, messageID int, revoke bool) error
	SetReaction(ctx context.Context, peer tg.InputPeerClass, messageID int, reactions []tg.ReactionClass) error
}

type gotdOutboundRPC struct {
	raw    *tg.Client
	rand   io.Reader
	sender *message.Sender
}

func newGotdOutboundRPC(client *gotdtelegram.Client) gotdOutboundRPC {
	raw := client.API()

	return gotdOutboundRPC{
		raw:    raw,
		rand:   crypto.DefaultRand(),
		sender: message.NewSender(raw),
	}
}

func (r gotdOutboundRPC) SendText(
	ctx context.Context,
	peer tg.InputPeerClass,
	request otogi.SendMessageRequest,
) (int, error) {
	entities, err := mapOutboundTextEntities(request.Text, request.Entities)
	if err != nil {
		return 0, fmt.Errorf("map outbound entities: %w", err)
	}

	sendRequest := &tg.MessagesSendMessageRequest{
		Peer:      peer,
		Message:   request.Text,
		NoWebpage: request.DisableLinkPreview,
		Silent:    request.Silent,
		Entities:  entities,
	}
	if request.ReplyToMessageID != "" {
		replyID, err := parseMessageID(request.ReplyToMessageID)
		if err != nil {
			return 0, fmt.Errorf("send text parse reply id %s: %w", request.ReplyToMessageID, err)
		}
		sendRequest.ReplyTo = &tg.InputReplyToMessage{
			ReplyToMsgID: replyID,
		}
	}

	randomID, err := crypto.RandInt64(r.rand)
	if err != nil {
		return 0, fmt.Errorf("send text random id: %w", err)
	}
	sendRequest.RandomID = randomID

	updates, err := r.raw.MessagesSendMessage(ctx, sendRequest)
	if err != nil {
		return 0, fmt.Errorf("send text: %w", err)
	}

	messageID, err := unpack.MessageID(updates, nil)
	if err != nil {
		return 0, fmt.Errorf("extract sent message id: %w", err)
	}

	return messageID, nil
}

func (r gotdOutboundRPC) EditText(
	ctx context.Context,
	peer tg.InputPeerClass,
	messageID int,
	request otogi.EditMessageRequest,
) error {
	entities, err := mapOutboundTextEntities(request.Text, request.Entities)
	if err != nil {
		return fmt.Errorf("map outbound entities: %w", err)
	}

	_, err = r.raw.MessagesEditMessage(ctx, &tg.MessagesEditMessageRequest{
		Peer:      peer,
		ID:        messageID,
		Message:   request.Text,
		NoWebpage: request.DisableLinkPreview,
		Entities:  entities,
	})
	if err != nil {
		return fmt.Errorf("edit text: %w", err)
	}

	return nil
}

func (r gotdOutboundRPC) DeleteMessage(
	ctx context.Context,
	peer tg.InputPeerClass,
	messageID int,
	revoke bool,
) error {
	if revoke {
		if _, err := r.sender.To(peer).Revoke().Messages(ctx, messageID); err != nil {
			return fmt.Errorf("revoke delete message: %w", err)
		}

		return nil
	}

	if _, isChannel := peer.(*tg.InputPeerChannel); isChannel {
		return fmt.Errorf("%w: non-revoke channel delete", otogi.ErrOutboundUnsupported)
	}

	if _, err := r.sender.Delete().Messages(ctx, messageID); err != nil {
		return fmt.Errorf("delete message: %w", err)
	}

	return nil
}

func (r gotdOutboundRPC) SetReaction(
	ctx context.Context,
	peer tg.InputPeerClass,
	messageID int,
	reactions []tg.ReactionClass,
) error {
	if _, err := r.sender.To(peer).Reaction(ctx, messageID, reactions...); err != nil {
		return fmt.Errorf("set reaction: %w", err)
	}

	return nil
}

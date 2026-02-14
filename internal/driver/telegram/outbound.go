package telegram

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"ex-otogi/pkg/otogi"

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

// OutboundDispatcher adapts neutral outbound operations to Telegram RPC calls.
type OutboundDispatcher struct {
	cfg      outboundConfig
	peers    *PeerCache
	telegram outboundRPC
}

type outboundConfig struct {
	rpcTimeout time.Duration
}

// NewOutboundDispatcher creates a Telegram outbound dispatcher using gotd client APIs.
func NewOutboundDispatcher(
	client *gotdtelegram.Client,
	peers *PeerCache,
	options ...OutboundOption,
) (*OutboundDispatcher, error) {
	if client == nil {
		return nil, fmt.Errorf("new telegram outbound dispatcher: nil client")
	}

	return newOutboundDispatcherWithRPC(newGotdOutboundRPC(client), peers, options...)
}

func newOutboundDispatcherWithRPC(
	rpc outboundRPC,
	peers *PeerCache,
	options ...OutboundOption,
) (*OutboundDispatcher, error) {
	if rpc == nil {
		return nil, fmt.Errorf("new telegram outbound dispatcher: nil rpc adapter")
	}
	if peers == nil {
		return nil, fmt.Errorf("new telegram outbound dispatcher: nil peer cache")
	}

	cfg := outboundConfig{
		rpcTimeout: defaultOutboundTimeout,
	}
	for _, option := range options {
		option(&cfg)
	}

	return &OutboundDispatcher{
		cfg:      cfg,
		peers:    peers,
		telegram: rpc,
	}, nil
}

// SendMessage publishes a text message to a Telegram conversation.
func (d *OutboundDispatcher) SendMessage(
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

	return &otogi.OutboundMessage{
		ID:     strconv.Itoa(id),
		Target: request.Target,
	}, nil
}

// EditMessage updates text for an existing Telegram message.
func (d *OutboundDispatcher) EditMessage(ctx context.Context, request otogi.EditMessageRequest) error {
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

	return nil
}

// DeleteMessage removes an existing Telegram message.
func (d *OutboundDispatcher) DeleteMessage(ctx context.Context, request otogi.DeleteMessageRequest) error {
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

	return nil
}

// SetReaction applies add/remove reaction behavior on an existing Telegram message.
func (d *OutboundDispatcher) SetReaction(ctx context.Context, request otogi.SetReactionRequest) error {
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

	return nil
}

func (d *OutboundDispatcher) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if d.cfg.rpcTimeout <= 0 {
		return ctx, func() {}
	}

	return context.WithTimeout(ctx, d.cfg.rpcTimeout)
}

func (d *OutboundDispatcher) resolvePeer(target otogi.OutboundTarget) (tg.InputPeerClass, error) {
	if target.Platform != otogi.PlatformTelegram {
		return nil, fmt.Errorf("%w: platform %s", otogi.ErrOutboundUnsupported, target.Platform)
	}

	peer, err := d.peers.Resolve(target.Conversation)
	if err != nil {
		return nil, fmt.Errorf("resolve conversation %s: %w", target.Conversation.ID, err)
	}

	return peer, nil
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

type outboundRPC interface {
	SendText(ctx context.Context, peer tg.InputPeerClass, request otogi.SendMessageRequest) (int, error)
	EditText(ctx context.Context, peer tg.InputPeerClass, messageID int, request otogi.EditMessageRequest) error
	DeleteMessage(ctx context.Context, peer tg.InputPeerClass, messageID int, revoke bool) error
	SetReaction(ctx context.Context, peer tg.InputPeerClass, messageID int, reactions []tg.ReactionClass) error
}

type gotdOutboundRPC struct {
	sender *message.Sender
}

func newGotdOutboundRPC(client *gotdtelegram.Client) gotdOutboundRPC {
	return gotdOutboundRPC{
		sender: message.NewSender(client.API()),
	}
}

func (r gotdOutboundRPC) SendText(
	ctx context.Context,
	peer tg.InputPeerClass,
	request otogi.SendMessageRequest,
) (int, error) {
	builder := r.sender.To(peer)
	if request.DisableLinkPreview {
		builder.NoWebpage()
	}
	if request.Silent {
		builder.Silent()
	}
	if request.ReplyToMessageID != "" {
		replyID, err := parseMessageID(request.ReplyToMessageID)
		if err != nil {
			return 0, fmt.Errorf("send text parse reply id %s: %w", request.ReplyToMessageID, err)
		}
		builder.Reply(replyID)
	}

	updates, err := builder.Text(ctx, request.Text)
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
	builder := r.sender.To(peer)
	if request.DisableLinkPreview {
		builder.NoWebpage()
	}

	if _, err := builder.Edit(messageID).Text(ctx, request.Text); err != nil {
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

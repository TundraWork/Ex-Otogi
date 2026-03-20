package discord

import (
	"context"
	"fmt"
	"time"

	"ex-otogi/pkg/otogi/platform"

	"github.com/bwmarrin/discordgo"
)

const (
	defaultOutboundRPCTimeout = 5 * time.Second
	discordMaxTimeoutDuration = 28 * 24 * time.Hour
	discordBotSelfUserID      = "@me"
)

// discordOutboundRPC abstracts the discordgo.Session outbound API methods
// needed by SinkDispatcher and ModerationDispatcher.
type discordOutboundRPC interface {
	// ChannelMessageSend sends a text message to the given channel.
	ChannelMessageSend(channelID, content string) (*discordgo.Message, error)
	// ChannelMessageEdit edits an existing message in the given channel.
	ChannelMessageEdit(channelID, messageID, content string) (*discordgo.Message, error)
	// ChannelMessageDelete deletes a message from the given channel.
	ChannelMessageDelete(channelID, messageID string) error
	// MessageReactionAdd adds an emoji reaction to a message.
	MessageReactionAdd(channelID, messageID, emojiID string) error
	// MessageReactionRemove removes an emoji reaction from a message.
	MessageReactionRemove(channelID, messageID, emojiID, userID string) error
	// GuildMemberTimeout applies or clears a communication timeout for a guild member.
	GuildMemberTimeout(guildID, userID string, until *time.Time) error
}

// discordgoOutboundAdapter wraps *discordgo.Session to satisfy discordOutboundRPC
// without exposing variadic RequestOption parameters.
type discordgoOutboundAdapter struct {
	session *discordgo.Session
}

func (a *discordgoOutboundAdapter) ChannelMessageSend(channelID, content string) (*discordgo.Message, error) {
	msg, err := a.session.ChannelMessageSend(channelID, content)
	if err != nil {
		return nil, fmt.Errorf("discordgo channel message send: %w", err)
	}

	return msg, nil
}

func (a *discordgoOutboundAdapter) ChannelMessageEdit(channelID, messageID, content string) (*discordgo.Message, error) {
	msg, err := a.session.ChannelMessageEdit(channelID, messageID, content)
	if err != nil {
		return nil, fmt.Errorf("discordgo channel message edit: %w", err)
	}

	return msg, nil
}

func (a *discordgoOutboundAdapter) ChannelMessageDelete(channelID, messageID string) error {
	if err := a.session.ChannelMessageDelete(channelID, messageID); err != nil {
		return fmt.Errorf("discordgo channel message delete: %w", err)
	}

	return nil
}

func (a *discordgoOutboundAdapter) MessageReactionAdd(channelID, messageID, emojiID string) error {
	if err := a.session.MessageReactionAdd(channelID, messageID, emojiID); err != nil {
		return fmt.Errorf("discordgo message reaction add: %w", err)
	}

	return nil
}

func (a *discordgoOutboundAdapter) MessageReactionRemove(channelID, messageID, emojiID, userID string) error {
	if err := a.session.MessageReactionRemove(channelID, messageID, emojiID, userID); err != nil {
		return fmt.Errorf("discordgo message reaction remove: %w", err)
	}

	return nil
}

func (a *discordgoOutboundAdapter) GuildMemberTimeout(guildID, userID string, until *time.Time) error {
	if err := a.session.GuildMemberTimeout(guildID, userID, until); err != nil {
		return fmt.Errorf("discordgo guild member timeout: %w", err)
	}

	return nil
}

// outboundConfig carries runtime parameters for Discord outbound dispatchers.
type outboundConfig struct {
	rpcTimeout time.Duration
	sink       platform.EventSink
}

// OutboundOption mutates Discord outbound dispatcher configuration.
type OutboundOption func(*outboundConfig)

// WithOutboundTimeout configures the timeout applied to each outbound RPC call.
func WithOutboundTimeout(timeout time.Duration) OutboundOption {
	return func(cfg *outboundConfig) {
		if timeout > 0 {
			cfg.rpcTimeout = timeout
		}
	}
}

// WithOutboundSinkRef configures the sink identity returned by sink-discovery calls.
func WithOutboundSinkRef(sink platform.EventSink) OutboundOption {
	return func(cfg *outboundConfig) {
		cfg.sink = sink
		if cfg.sink.Platform == "" {
			cfg.sink.Platform = DriverPlatform
		}
	}
}

// SinkDispatcher adapts Otogi outbound operations to Discord REST API calls.
//
// Text is rendered from TextEntity annotations to Discord Markdown. Messages
// exceeding the 2000-character limit are split at line boundaries for SendMessage
// and truncated with a suffix marker for EditMessage.
type SinkDispatcher struct {
	cfg        platform.EventSink
	rpc        discordOutboundRPC
	rpcTimeout time.Duration
}

// NewSinkDispatcher creates a Discord SinkDispatcher using a discordgo.Session.
func NewSinkDispatcher(
	session *discordgo.Session,
	options ...OutboundOption,
) (*SinkDispatcher, error) {
	if session == nil {
		return nil, fmt.Errorf("new discord sink dispatcher: nil session")
	}

	return newSinkDispatcherWithRPC(&discordgoOutboundAdapter{session: session}, options...)
}

func newSinkDispatcherWithRPC(rpc discordOutboundRPC, options ...OutboundOption) (*SinkDispatcher, error) {
	if rpc == nil {
		return nil, fmt.Errorf("new discord sink dispatcher: nil rpc")
	}

	cfg := outboundConfig{
		rpcTimeout: defaultOutboundRPCTimeout,
		sink: platform.EventSink{
			Platform: DriverPlatform,
		},
	}
	for _, opt := range options {
		opt(&cfg)
	}

	return &SinkDispatcher{
		cfg:        cfg.sink,
		rpc:        rpc,
		rpcTimeout: cfg.rpcTimeout,
	}, nil
}

// SendMessage sends one or more Discord messages, splitting when the rendered
// text exceeds the 2000-character limit. It returns the first message ID.
func (d *SinkDispatcher) SendMessage(ctx context.Context, request platform.SendMessageRequest) (*platform.OutboundMessage, error) {
	if err := request.Validate(); err != nil {
		return nil, fmt.Errorf("discord send message: %w", err)
	}

	rendered := renderDiscordMarkdown(request.Text, request.Entities)
	parts := splitDiscordMessage(rendered)

	rpcCtx, cancel := context.WithTimeout(ctx, d.rpcTimeout*time.Duration(len(parts)))
	defer cancel()

	var firstID string
	channelID := request.Target.Conversation.ID
	for i, part := range parts {
		msg, err := d.rpc.ChannelMessageSend(channelID, part)
		if err != nil {
			return nil, fmt.Errorf("discord send message part %d: %w",
				i, mapDiscordOutboundError(platform.OutboundOperationSendMessage, d.cfg, err))
		}
		if i == 0 && msg != nil {
			firstID = msg.ID
		}
	}
	_ = rpcCtx

	return &platform.OutboundMessage{
		ID:     firstID,
		Target: request.Target,
		Tags:   request.Tags,
	}, nil
}

// EditMessage replaces the content of an existing Discord message.
// Text is truncated with a suffix if it exceeds the 2000-character limit.
func (d *SinkDispatcher) EditMessage(ctx context.Context, request platform.EditMessageRequest) error {
	if err := request.Validate(); err != nil {
		return fmt.Errorf("discord edit message: %w", err)
	}

	rendered := renderDiscordMarkdown(request.Text, request.Entities)
	content := truncateDiscordEdit(rendered)

	rpcCtx, cancel := context.WithTimeout(ctx, d.rpcTimeout)
	defer cancel()

	channelID := request.Target.Conversation.ID
	if _, err := d.rpc.ChannelMessageEdit(channelID, request.MessageID, content); err != nil {
		return fmt.Errorf("discord edit message %s: %w",
			request.MessageID, mapDiscordOutboundError(platform.OutboundOperationEditMessage, d.cfg, err))
	}

	_ = rpcCtx

	return nil
}

// DeleteMessage removes an existing Discord message.
func (d *SinkDispatcher) DeleteMessage(ctx context.Context, request platform.DeleteMessageRequest) error {
	if err := request.Validate(); err != nil {
		return fmt.Errorf("discord delete message: %w", err)
	}

	rpcCtx, cancel := context.WithTimeout(ctx, d.rpcTimeout)
	defer cancel()

	channelID := request.Target.Conversation.ID
	if err := d.rpc.ChannelMessageDelete(channelID, request.MessageID); err != nil {
		return fmt.Errorf("discord delete message %s: %w",
			request.MessageID, mapDiscordOutboundError(platform.OutboundOperationDeleteMessage, d.cfg, err))
	}

	_ = rpcCtx

	return nil
}

// SetReaction adds or removes an emoji reaction on a Discord message.
// Removal always targets the bot's own reaction (@me).
func (d *SinkDispatcher) SetReaction(ctx context.Context, request platform.SetReactionRequest) error {
	if err := request.Validate(); err != nil {
		return fmt.Errorf("discord set reaction: %w", err)
	}

	rpcCtx, cancel := context.WithTimeout(ctx, d.rpcTimeout)
	defer cancel()

	channelID := request.Target.Conversation.ID

	var err error
	if request.Action == platform.ReactionActionAdd {
		err = d.rpc.MessageReactionAdd(channelID, request.MessageID, request.Emoji)
	} else {
		err = d.rpc.MessageReactionRemove(channelID, request.MessageID, request.Emoji, discordBotSelfUserID)
	}

	_ = rpcCtx

	if err != nil {
		return fmt.Errorf("discord set reaction on message %s: %w",
			request.MessageID, mapDiscordOutboundError(platform.OutboundOperationSetReaction, d.cfg, err))
	}

	return nil
}

// ListSinks returns all active sink identities for this Discord runtime.
func (d *SinkDispatcher) ListSinks(_ context.Context) ([]platform.EventSink, error) {
	return []platform.EventSink{d.cfg}, nil
}

// ListSinksByPlatform returns active sink identities for one platform.
func (d *SinkDispatcher) ListSinksByPlatform(_ context.Context, p platform.Platform) ([]platform.EventSink, error) {
	if p == DriverPlatform || p == "" {
		return []platform.EventSink{d.cfg}, nil
	}

	return nil, nil
}

// ModerationDispatcher adapts Otogi moderation operations to Discord REST API calls.
//
// Discord's communication timeout (communication_disabled_until) is the closest
// mapping to a generic member restriction. The maximum timeout duration is 28 days.
// Passing fully-granted permissions clears the timeout; any other permission set
// applies the timeout until UntilDate (or the maximum allowed duration when zero).
type ModerationDispatcher struct {
	cfg        platform.EventSink
	rpc        discordOutboundRPC
	rpcTimeout time.Duration
}

// NewModerationDispatcher creates a Discord ModerationDispatcher using a discordgo.Session.
func NewModerationDispatcher(
	session *discordgo.Session,
	options ...OutboundOption,
) (*ModerationDispatcher, error) {
	if session == nil {
		return nil, fmt.Errorf("new discord moderation dispatcher: nil session")
	}

	return newModerationDispatcherWithRPC(&discordgoOutboundAdapter{session: session}, options...)
}

func newModerationDispatcherWithRPC(rpc discordOutboundRPC, options ...OutboundOption) (*ModerationDispatcher, error) {
	if rpc == nil {
		return nil, fmt.Errorf("new discord moderation dispatcher: nil rpc")
	}

	cfg := outboundConfig{
		rpcTimeout: defaultOutboundRPCTimeout,
		sink: platform.EventSink{
			Platform: DriverPlatform,
		},
	}
	for _, opt := range options {
		opt(&cfg)
	}

	return &ModerationDispatcher{
		cfg:        cfg.sink,
		rpc:        rpc,
		rpcTimeout: cfg.rpcTimeout,
	}, nil
}

// RestrictMember applies a Discord communication timeout to the target member.
//
// When all permissions are granted, the timeout is cleared. Otherwise the timeout
// is applied until UntilDate (clamped to 28 days from now when zero or beyond the
// maximum).
func (d *ModerationDispatcher) RestrictMember(ctx context.Context, request platform.RestrictMemberRequest) error {
	if err := request.Validate(); err != nil {
		return fmt.Errorf("discord restrict member: %w", err)
	}

	guildID := request.Target.Conversation.ID
	until := discordTimeoutUntil(request.Permissions, request.UntilDate)

	rpcCtx, cancel := context.WithTimeout(ctx, d.rpcTimeout)
	defer cancel()

	if err := d.rpc.GuildMemberTimeout(guildID, request.MemberID, until); err != nil {
		return fmt.Errorf("discord restrict member %s: %w",
			request.MemberID, mapDiscordOutboundError(platform.OutboundOperationRestrictMember, d.cfg, err))
	}

	_ = rpcCtx

	return nil
}

// discordTimeoutUntil computes the timeout expiry time for a Discord member restriction.
// Returns nil to clear an existing timeout when full permissions are granted.
// Returns a clamped future time otherwise.
func discordTimeoutUntil(perms platform.MemberPermissions, until time.Time) *time.Time {
	if perms == platform.AllPermissionsGranted() {
		// Clear the timeout.
		return nil
	}

	now := time.Now().UTC()
	maxUntil := now.Add(discordMaxTimeoutDuration)

	if until.IsZero() || until.After(maxUntil) {
		until = maxUntil
	}

	if until.Before(now) {
		until = now.Add(time.Second)
	}

	return &until
}

// Compile-time interface assertions.
var (
	_ platform.SinkDispatcher       = (*SinkDispatcher)(nil)
	_ platform.ModerationDispatcher = (*ModerationDispatcher)(nil)
)

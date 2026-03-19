package discord

import (
	"context"
	"strings"
	"time"

	"ex-otogi/pkg/otogi/platform"

	"github.com/bwmarrin/discordgo"
)

// DefaultDiscordgoMapper maps raw discordgo gateway events to adapter Update DTOs.
type DefaultDiscordgoMapper struct{}

// NewDefaultDiscordgoMapper creates the default discordgo event mapper.
func NewDefaultDiscordgoMapper() DefaultDiscordgoMapper {
	return DefaultDiscordgoMapper{}
}

// Map converts a raw discordgo gateway event to an adapter Update DTO.
// Returns accepted=false for unsupported event types.
func (m DefaultDiscordgoMapper) Map(_ context.Context, raw any) (Update, bool, error) {
	switch event := raw.(type) {
	case *discordgo.MessageCreate:
		if event.Message == nil {
			return Update{}, false, nil
		}
		return mapDiscordMessageCreate(event.Message), true, nil
	case *discordgo.MessageUpdate:
		if event.Message == nil {
			return Update{}, false, nil
		}
		return mapDiscordMessageUpdate(event), true, nil
	case *discordgo.MessageDelete:
		if event.Message == nil {
			return Update{}, false, nil
		}
		return mapDiscordMessageDelete(event), true, nil
	case *discordgo.MessageReactionAdd:
		if event.MessageReaction == nil {
			return Update{}, false, nil
		}
		return mapDiscordReactionAdd(event), true, nil
	case *discordgo.MessageReactionRemove:
		if event.MessageReaction == nil {
			return Update{}, false, nil
		}
		return mapDiscordReactionRemove(event), true, nil
	case *discordgo.GuildMemberAdd:
		if event.Member == nil {
			return Update{}, false, nil
		}
		return mapDiscordMemberAdd(event), true, nil
	case *discordgo.GuildMemberRemove:
		if event.Member == nil {
			return Update{}, false, nil
		}
		return mapDiscordMemberRemove(event), true, nil
	default:
		return Update{}, false, nil
	}
}

func mapDiscordMessageCreate(msg *discordgo.Message) Update {
	return Update{
		ID:         msg.ID,
		Type:       UpdateTypeMessage,
		OccurredAt: msg.Timestamp,
		Channel:    discordChannelRefFromMessage(msg),
		Actor:      discordActorRefFromUser(msg.Author),
		Article:    discordArticlePayloadFromMessage(msg),
	}
}

func mapDiscordMessageUpdate(event *discordgo.MessageUpdate) Update {
	msg := event.Message
	editedAt := msg.EditedTimestamp

	var before *ArticleSnapshotPayload
	if event.BeforeUpdate != nil {
		before = &ArticleSnapshotPayload{
			Text:  event.BeforeUpdate.Content,
			Media: discordMediaFromAttachments(event.BeforeUpdate.Attachments),
		}
	}
	after := &ArticleSnapshotPayload{
		Text:  msg.Content,
		Media: discordMediaFromAttachments(msg.Attachments),
	}

	occurredAt := msg.Timestamp
	if editedAt != nil {
		occurredAt = *editedAt
	}

	return Update{
		ID:         msg.ID,
		Type:       UpdateTypeEdit,
		OccurredAt: occurredAt,
		Channel:    discordChannelRefFromMessage(msg),
		Actor:      discordActorRefFromUser(msg.Author),
		Edit: &ArticleEditPayload{
			ArticleID: msg.ID,
			ChangedAt: editedAt,
			Before:    before,
			After:     after,
		},
	}
}

func mapDiscordMessageDelete(event *discordgo.MessageDelete) Update {
	msg := event.Message
	return Update{
		ID:         msg.ID,
		Type:       UpdateTypeDelete,
		OccurredAt: time.Now().UTC(),
		Channel:    discordChannelRefFromMessage(msg),
		Delete: &ArticleDeletePayload{
			ArticleID: msg.ID,
		},
	}
}

func mapDiscordReactionAdd(event *discordgo.MessageReactionAdd) Update {
	reaction := event.MessageReaction
	actor := ActorRef{ID: reaction.UserID}
	if event.Member != nil && event.Member.User != nil {
		actor = discordActorRefFromUser(event.Member.User)
		actor.ID = reaction.UserID
	}

	return Update{
		ID:         discordReactionUpdateID("add", reaction),
		Type:       UpdateTypeReactionAdd,
		OccurredAt: time.Now().UTC(),
		Channel:    discordChannelRefFromReaction(reaction),
		Actor:      actor,
		Reaction: &ArticleReactionPayload{
			ArticleID: reaction.MessageID,
			Emoji:     discordNormalizeEmoji(reaction.Emoji),
		},
	}
}

func mapDiscordReactionRemove(event *discordgo.MessageReactionRemove) Update {
	reaction := event.MessageReaction
	return Update{
		ID:         discordReactionUpdateID("rm", reaction),
		Type:       UpdateTypeReactionRemove,
		OccurredAt: time.Now().UTC(),
		Channel:    discordChannelRefFromReaction(reaction),
		Actor:      ActorRef{ID: reaction.UserID},
		Reaction: &ArticleReactionPayload{
			ArticleID: reaction.MessageID,
			Emoji:     discordNormalizeEmoji(reaction.Emoji),
		},
	}
}

func mapDiscordMemberAdd(event *discordgo.GuildMemberAdd) Update {
	member := event.Member
	actor := discordActorRefFromMember(member)
	guildRef := ChannelRef{
		ID:      member.GuildID,
		GuildID: member.GuildID,
		Type:    platform.ConversationTypeGroup,
	}

	return Update{
		ID:         "join:" + member.GuildID + ":" + actor.ID,
		Type:       UpdateTypeMemberJoin,
		OccurredAt: member.JoinedAt,
		Channel:    guildRef,
		Actor:      actor,
		Member: &MemberPayload{
			Member:   actor,
			JoinedAt: member.JoinedAt,
		},
	}
}

func mapDiscordMemberRemove(event *discordgo.GuildMemberRemove) Update {
	member := event.Member
	actor := discordActorRefFromMember(member)
	guildRef := ChannelRef{
		ID:      member.GuildID,
		GuildID: member.GuildID,
		Type:    platform.ConversationTypeGroup,
	}

	return Update{
		ID:         "leave:" + member.GuildID + ":" + actor.ID,
		Type:       UpdateTypeMemberLeave,
		OccurredAt: time.Now().UTC(),
		Channel:    guildRef,
		Actor:      actor,
		Member: &MemberPayload{
			Member:   actor,
			JoinedAt: member.JoinedAt,
		},
	}
}

// discordChannelRefFromMessage derives a ChannelRef from a Discord message.
func discordChannelRefFromMessage(msg *discordgo.Message) ChannelRef {
	return ChannelRef{
		ID:      msg.ChannelID,
		GuildID: msg.GuildID,
		Type:    discordConversationTypeFromGuild(msg.GuildID),
	}
}

// discordChannelRefFromReaction derives a ChannelRef from a Discord reaction.
func discordChannelRefFromReaction(reaction *discordgo.MessageReaction) ChannelRef {
	return ChannelRef{
		ID:      reaction.ChannelID,
		GuildID: reaction.GuildID,
		Type:    discordConversationTypeFromGuild(reaction.GuildID),
	}
}

// discordConversationTypeFromGuild maps guild presence to conversation type.
// An empty guildID means the message is from a DM (private conversation).
func discordConversationTypeFromGuild(guildID string) platform.ConversationType {
	if guildID == "" {
		return platform.ConversationTypePrivate
	}

	return platform.ConversationTypeGroup
}

// discordActorRefFromUser converts a discordgo.User to an ActorRef.
func discordActorRefFromUser(user *discordgo.User) ActorRef {
	if user == nil {
		return ActorRef{}
	}

	displayName := user.GlobalName
	if displayName == "" {
		displayName = user.Username
	}

	return ActorRef{
		ID:          user.ID,
		Username:    user.Username,
		DisplayName: displayName,
		IsBot:       user.Bot,
	}
}

// discordActorRefFromMember converts a discordgo.Member to an ActorRef.
// The guild nickname takes precedence over the global display name when present.
func discordActorRefFromMember(member *discordgo.Member) ActorRef {
	if member == nil {
		return ActorRef{}
	}

	actor := discordActorRefFromUser(member.User)
	if member.Nick != "" {
		actor.DisplayName = member.Nick
	}

	return actor
}

// discordArticlePayloadFromMessage builds an ArticlePayload from a Discord message.
func discordArticlePayloadFromMessage(msg *discordgo.Message) *ArticlePayload {
	var replyToID string
	if msg.MessageReference != nil {
		replyToID = msg.MessageReference.MessageID
	}

	return &ArticlePayload{
		ID:               msg.ID,
		ReplyToArticleID: replyToID,
		Text:             msg.Content,
		Media:            discordMediaFromAttachments(msg.Attachments),
	}
}

// discordMediaFromAttachments maps Discord attachments to MediaPayload values.
func discordMediaFromAttachments(attachments []*discordgo.MessageAttachment) []MediaPayload {
	if len(attachments) == 0 {
		return nil
	}

	result := make([]MediaPayload, 0, len(attachments))
	for _, att := range attachments {
		result = append(result, MediaPayload{
			ID:        att.ID,
			Type:      discordMediaTypeFromAttachment(att),
			MIMEType:  att.ContentType,
			FileName:  att.Filename,
			SizeBytes: int64(att.Size),
			URI:       att.URL,
		})
	}

	return result
}

// discordMediaTypeFromAttachment classifies a Discord attachment by its content type.
func discordMediaTypeFromAttachment(att *discordgo.MessageAttachment) platform.MediaType {
	ct := strings.ToLower(att.ContentType)
	switch {
	case strings.HasPrefix(ct, "image/"):
		return platform.MediaTypePhoto
	case strings.HasPrefix(ct, "video/"):
		return platform.MediaTypeVideo
	case strings.HasPrefix(ct, "audio/"):
		return platform.MediaTypeAudio
	default:
		return platform.MediaTypeDocument
	}
}

// discordNormalizeEmoji converts a discordgo.Emoji to a stable string token.
// Custom emojis are represented as "name:id"; standard Unicode emojis use their name/symbol directly.
func discordNormalizeEmoji(emoji discordgo.Emoji) string {
	if emoji.ID != "" {
		return emoji.Name + ":" + emoji.ID
	}

	return emoji.Name
}

// discordReactionUpdateID builds a stable identifier for a reaction event.
func discordReactionUpdateID(action string, reaction *discordgo.MessageReaction) string {
	return action + ":" + reaction.ChannelID + ":" + reaction.MessageID + ":" +
		reaction.UserID + ":" + discordNormalizeEmoji(reaction.Emoji)
}

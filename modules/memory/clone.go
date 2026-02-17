package memory

import "ex-otogi/pkg/otogi"

func cloneMemorySnapshot(cached memorySnapshot) memorySnapshot {
	cloned := cached
	cloned.Article = cloneArticle(cached.Article)

	return cloned
}

func cloneEvent(event otogi.Event) otogi.Event {
	cloned := event
	if event.Article != nil {
		article := cloneArticle(*event.Article)
		cloned.Article = &article
	}
	if event.Mutation != nil {
		cloned.Mutation = cloneMutation(event.Mutation)
	}
	if event.Reaction != nil {
		reaction := *event.Reaction
		cloned.Reaction = &reaction
	}
	if event.StateChange != nil {
		cloned.StateChange = cloneStateChange(event.StateChange)
	}
	if len(event.Metadata) > 0 {
		cloned.Metadata = make(map[string]string, len(event.Metadata))
		for key, value := range event.Metadata {
			cloned.Metadata[key] = value
		}
	}

	return cloned
}

func cloneEventStream(events []otogi.Event) []otogi.Event {
	if len(events) == 0 {
		return nil
	}

	cloned := make([]otogi.Event, len(events))
	for idx, event := range events {
		cloned[idx] = cloneEvent(event)
	}

	return cloned
}

func cloneMutation(mutation *otogi.ArticleMutation) *otogi.ArticleMutation {
	if mutation == nil {
		return nil
	}
	cloned := *mutation
	if mutation.ChangedAt != nil {
		changedAt := *mutation.ChangedAt
		cloned.ChangedAt = &changedAt
	}
	if mutation.Before != nil {
		before := cloneArticleSnapshot(*mutation.Before)
		cloned.Before = &before
	}
	if mutation.After != nil {
		after := cloneArticleSnapshot(*mutation.After)
		cloned.After = &after
	}

	return &cloned
}

func cloneArticleSnapshot(snapshot otogi.ArticleSnapshot) otogi.ArticleSnapshot {
	cloned := snapshot
	if len(snapshot.Entities) > 0 {
		cloned.Entities = append([]otogi.TextEntity(nil), snapshot.Entities...)
	} else {
		cloned.Entities = nil
	}
	if len(snapshot.Media) > 0 {
		cloned.Media = cloneMediaAttachments(snapshot.Media)
	} else {
		cloned.Media = nil
	}

	return cloned
}

func cloneStateChange(state *otogi.StateChange) *otogi.StateChange {
	if state == nil {
		return nil
	}
	cloned := *state
	if state.Member != nil {
		member := *state.Member
		if state.Member.Inviter != nil {
			inviter := *state.Member.Inviter
			member.Inviter = &inviter
		}
		cloned.Member = &member
	}
	if state.Role != nil {
		role := *state.Role
		cloned.Role = &role
	}
	if state.Migration != nil {
		migration := *state.Migration
		cloned.Migration = &migration
	}

	return &cloned
}

func cloneArticle(article otogi.Article) otogi.Article {
	cloned := article
	if len(article.Entities) > 0 {
		cloned.Entities = append([]otogi.TextEntity(nil), article.Entities...)
	} else {
		cloned.Entities = nil
	}
	if len(article.Media) > 0 {
		cloned.Media = cloneMediaAttachments(article.Media)
	} else {
		cloned.Media = nil
	}
	if len(article.Reactions) > 0 {
		cloned.Reactions = cloneArticleReactions(article.Reactions)
	} else {
		cloned.Reactions = nil
	}

	return cloned
}

func cloneArticleReactions(reactions []otogi.ArticleReaction) []otogi.ArticleReaction {
	if len(reactions) == 0 {
		return nil
	}

	cloned := make([]otogi.ArticleReaction, len(reactions))
	copy(cloned, reactions)

	return cloned
}

func applyReactionToArticle(article *otogi.Article, reaction otogi.Reaction) {
	if article == nil {
		return
	}
	if reaction.Emoji == "" {
		return
	}

	index := -1
	for idx := range article.Reactions {
		if article.Reactions[idx].Emoji == reaction.Emoji {
			index = idx
			break
		}
	}

	switch reaction.Action {
	case otogi.ReactionActionAdd:
		if index >= 0 {
			article.Reactions[index].Count++
			return
		}
		article.Reactions = append(article.Reactions, otogi.ArticleReaction{
			Emoji: reaction.Emoji,
			Count: 1,
		})
	case otogi.ReactionActionRemove:
		if index < 0 {
			return
		}
		if article.Reactions[index].Count <= 1 {
			article.Reactions = append(article.Reactions[:index], article.Reactions[index+1:]...)
			return
		}
		article.Reactions[index].Count--
	default:
	}
}

func applyReactionHistoryToArticle(article *otogi.Article, history []otogi.Event, articleID string) {
	if article == nil || len(history) == 0 {
		return
	}

	for _, event := range history {
		if event.Reaction == nil {
			continue
		}
		if event.Kind != otogi.EventKindArticleReactionAdded && event.Kind != otogi.EventKindArticleReactionRemoved {
			continue
		}
		if articleID != "" && event.Reaction.ArticleID != "" && event.Reaction.ArticleID != articleID {
			continue
		}
		applyReactionToArticle(article, *event.Reaction)
	}
}

func cloneMediaAttachments(media []otogi.MediaAttachment) []otogi.MediaAttachment {
	if len(media) == 0 {
		return nil
	}

	cloned := make([]otogi.MediaAttachment, len(media))
	for idx, attachment := range media {
		attachmentClone := attachment
		if attachment.Preview != nil {
			previewClone := *attachment.Preview
			if len(attachment.Preview.Bytes) > 0 {
				previewClone.Bytes = append([]byte(nil), attachment.Preview.Bytes...)
			}
			attachmentClone.Preview = &previewClone
		}
		cloned[idx] = attachmentClone
	}

	return cloned
}

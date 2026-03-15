package eventcache

import "ex-otogi/pkg/otogi/platform"

func cloneMemorySnapshot(cached memorySnapshot) memorySnapshot {
	cloned := cached
	cloned.Article = cloneArticle(cached.Article)

	return cloned
}

func cloneEvent(event platform.Event) platform.Event {
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

func cloneEventStream(events []platform.Event) []platform.Event {
	if len(events) == 0 {
		return nil
	}

	cloned := make([]platform.Event, len(events))
	for idx, event := range events {
		cloned[idx] = cloneEvent(event)
	}

	return cloned
}

func cloneMutation(mutation *platform.ArticleMutation) *platform.ArticleMutation {
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

func cloneArticleSnapshot(snapshot platform.ArticleSnapshot) platform.ArticleSnapshot {
	cloned := snapshot
	if len(snapshot.Entities) > 0 {
		cloned.Entities = append([]platform.TextEntity(nil), snapshot.Entities...)
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

func cloneStateChange(state *platform.StateChange) *platform.StateChange {
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

func cloneArticle(article platform.Article) platform.Article {
	cloned := article
	if len(article.Entities) > 0 {
		cloned.Entities = append([]platform.TextEntity(nil), article.Entities...)
	} else {
		cloned.Entities = nil
	}
	if len(article.Media) > 0 {
		cloned.Media = cloneMediaAttachments(article.Media)
	} else {
		cloned.Media = nil
	}
	if len(article.Tags) > 0 {
		cloned.Tags = make(map[string]string, len(article.Tags))
		for key, value := range article.Tags {
			cloned.Tags[key] = value
		}
	} else {
		cloned.Tags = nil
	}
	if len(article.Reactions) > 0 {
		cloned.Reactions = cloneArticleReactions(article.Reactions)
	} else {
		cloned.Reactions = nil
	}

	return cloned
}

func cloneArticleReactions(reactions []platform.ArticleReaction) []platform.ArticleReaction {
	if len(reactions) == 0 {
		return nil
	}

	cloned := make([]platform.ArticleReaction, len(reactions))
	copy(cloned, reactions)

	return cloned
}

func applyReactionToArticle(article *platform.Article, reaction platform.Reaction) {
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
	case platform.ReactionActionAdd:
		if index >= 0 {
			article.Reactions[index].Count++
			return
		}
		article.Reactions = append(article.Reactions, platform.ArticleReaction{
			Emoji: reaction.Emoji,
			Count: 1,
		})
	case platform.ReactionActionRemove:
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

func applyReactionHistoryToArticle(article *platform.Article, history []platform.Event, articleID string) {
	if article == nil || len(history) == 0 {
		return
	}

	for _, event := range history {
		if event.Reaction == nil {
			continue
		}
		if event.Kind != platform.EventKindArticleReactionAdded && event.Kind != platform.EventKindArticleReactionRemoved {
			continue
		}
		if articleID != "" && event.Reaction.ArticleID != "" && event.Reaction.ArticleID != articleID {
			continue
		}
		applyReactionToArticle(article, *event.Reaction)
	}
}

// mergeArticleTags merges source tags into existing tags. Existing keys are
// not overwritten so that previously projected tags take precedence.
func mergeArticleTags(existing map[string]string, source map[string]string) map[string]string {
	if len(source) == 0 {
		return existing
	}
	if len(existing) == 0 {
		merged := make(map[string]string, len(source))
		for key, value := range source {
			merged[key] = value
		}
		return merged
	}

	merged := make(map[string]string, len(existing)+len(source))
	for key, value := range existing {
		merged[key] = value
	}
	for key, value := range source {
		if _, exists := merged[key]; !exists {
			merged[key] = value
		}
	}

	return merged
}

func cloneMediaAttachments(media []platform.MediaAttachment) []platform.MediaAttachment {
	if len(media) == 0 {
		return nil
	}

	cloned := make([]platform.MediaAttachment, len(media))
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

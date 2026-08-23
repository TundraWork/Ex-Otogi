package memory

import (
	"context"
	"fmt"
	"html"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"ex-otogi/pkg/otogi/ai"
	"ex-otogi/pkg/otogi/core"
	"ex-otogi/pkg/otogi/platform"
)

// collectPendingReplyLookups scans the buffered articles for ReplyToArticleID
// references that are NOT already present in the buffer itself, and produces
// a deduped list of memory lookups to resolve them.
func collectPendingReplyLookups(
	scope ai.SemanticScope,
	articles []bufferedArticle,
) []core.MemoryLookup {
	bufferIDs := make(map[string]struct{}, len(articles))
	for _, a := range articles {
		if id := strings.TrimSpace(a.Article.ID); id != "" {
			bufferIDs[id] = struct{}{}
		}
	}

	seen := make(map[string]struct{})
	var lookups []core.MemoryLookup

	for _, a := range articles {
		replyTo := strings.TrimSpace(a.Article.ReplyToArticleID)
		if replyTo == "" {
			continue
		}
		if _, inBuffer := bufferIDs[replyTo]; inBuffer {
			continue
		}
		if _, already := seen[replyTo]; already {
			continue
		}
		seen[replyTo] = struct{}{}
		lookups = append(lookups, core.MemoryLookup{
			TenantID:       scope.TenantID,
			Platform:       platform.Platform(scope.Platform),
			ConversationID: scope.ConversationID,
			ArticleID:      replyTo,
		})
	}

	return lookups
}

// loadReplyTargets resolves a batch of reply lookups in one GetBatch call.
// Returns map[articleID]core.Memory. Missing entries are absent from the map.
func loadReplyTargets(
	ctx context.Context,
	memory core.MemoryService,
	lookups []core.MemoryLookup,
) (map[string]core.Memory, error) {
	if len(lookups) == 0 {
		return nil, nil
	}

	batch, err := memory.GetBatch(ctx, lookups)
	if err != nil {
		return nil, fmt.Errorf("load reply targets: %w", err)
	}
	if len(batch) == 0 {
		return nil, nil
	}

	result := make(map[string]core.Memory, len(batch))
	for lookup, mem := range batch {
		result[lookup.ArticleID] = mem
	}

	return result, nil
}

// serializeWindowConversation builds the <conversation> XML block for the
// extraction prompt. Articles render chronologically (by OccurredAt). Each
// article that has a resolvable reply target outside the buffer gets a
// <quoted_reply> element rendered inline above the article body.
//
// Output is rune-bounded by maxRunes. When the budget is exceeded, oldest
// articles are dropped from the head. The omittedHead return value counts
// how many articles were dropped.
func serializeWindowConversation(
	articles []bufferedArticle,
	quotes map[string]core.Memory,
	maxRunes int,
) (text string, omittedHead int) {
	if maxRunes <= 0 || len(articles) == 0 {
		return "", len(articles)
	}

	sorted := make([]bufferedArticle, len(articles))
	copy(sorted, articles)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].OccurredAt.Before(sorted[j].OccurredAt)
	})

	// Build per-article blocks (each block is the optional quoted_reply + message).
	blocks := make([]string, len(sorted))
	for i, a := range sorted {
		var b strings.Builder
		replyTo := strings.TrimSpace(a.Article.ReplyToArticleID)
		if replyTo != "" {
			if quote, found := quotes[replyTo]; found {
				quoteText := strings.TrimSpace(quote.Article.Text)
				if quoteText != "" {
					b.WriteString("<quoted_reply")
					b.WriteString(` article_id="`)
					b.WriteString(html.EscapeString(quote.Article.ID))
					b.WriteString(`"`)
					b.WriteString(` created_at="`)
					b.WriteString(quote.CreatedAt.UTC().Format(time.RFC3339))
					b.WriteString(`"`)
					b.WriteString(` actor_id="`)
					b.WriteString(html.EscapeString(quote.Actor.ID))
					b.WriteString(`"`)
					b.WriteString(` actor_name="`)
					b.WriteString(html.EscapeString(resolveActorName(quote.Actor)))
					b.WriteString(`"`)
					b.WriteString(">\n")
					b.WriteString(html.EscapeString(quoteText))
					b.WriteString("\n</quoted_reply>\n")
				}
			}
		}

		b.WriteString("<message")
		b.WriteString(` article_id="`)
		b.WriteString(html.EscapeString(a.Article.ID))
		b.WriteString(`"`)
		b.WriteString(` created_at="`)
		b.WriteString(a.OccurredAt.UTC().Format(time.RFC3339))
		b.WriteString(`"`)
		if replyTo != "" {
			b.WriteString(` reply_to="`)
			b.WriteString(html.EscapeString(replyTo))
			b.WriteString(`"`)
		}
		b.WriteString(` actor_id="`)
		b.WriteString(html.EscapeString(a.Actor.ID))
		b.WriteString(`"`)
		b.WriteString(` actor_name="`)
		b.WriteString(html.EscapeString(resolveActorName(a.Actor)))
		b.WriteString(`"`)
		b.WriteString(fmt.Sprintf(` is_bot="%t"`, a.Actor.IsBot))
		b.WriteString(">\n")
		b.WriteString(html.EscapeString(strings.TrimSpace(a.Article.Text)))
		b.WriteString("\n</message>")

		blocks[i] = b.String()
	}

	// Try the full output first; drop oldest blocks until it fits.
	const envelope = "<conversation>\n"
	const envelopeClose = "\n</conversation>"

	for drop := 0; drop <= len(blocks); drop++ {
		if drop == len(blocks) {
			return "", len(articles)
		}
		inner := strings.Join(blocks[drop:], "\n")
		full := envelope + inner + envelopeClose
		if utf8.RuneCountInString(full) <= maxRunes {
			return full, drop
		}
	}

	return "", len(articles)
}

// participantsFromArticles extracts unique actor refs from the buffered
// articles and resolved quotes for use in the extraction prompt.
func participantsFromArticles(
	articles []bufferedArticle,
	quotes map[string]core.Memory,
) []ai.SemanticActorRef {
	seen := make(map[string]struct{})
	var participants []ai.SemanticActorRef

	appendActor := func(actor platform.Actor) {
		ref := memoryActorRef(actor)
		if ref == nil {
			return
		}
		key := strings.TrimSpace(ref.ID) + "\x00" + strings.ToLower(strings.TrimSpace(ref.Name))
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		participants = append(participants, *ref)
	}

	for _, a := range articles {
		appendActor(a.Actor)
	}
	for _, mem := range quotes {
		appendActor(mem.Actor)
	}

	return participants
}

// resolveActorName returns the best available display name for an actor.
// It prefers DisplayName, then Username, then ID, then "unknown".
func resolveActorName(actor platform.Actor) string {
	if name := strings.TrimSpace(actor.DisplayName); name != "" {
		return name
	}
	if name := strings.TrimSpace(actor.Username); name != "" {
		return name
	}
	if id := strings.TrimSpace(actor.ID); id != "" {
		return id
	}

	return "unknown"
}

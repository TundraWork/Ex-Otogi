package llmchat

import (
	"context"
	"fmt"
	"strings"

	"ex-otogi/pkg/otogi/ai"
	"ex-otogi/pkg/otogi/platform"
)

// retrieveSemanticMemoriesViaService delegates semantic memory retrieval to the
// registered SemanticRetriever service. Returns an empty string when the agent
// has no retrieval configured or the service cannot serve the request.
func (m *Module) retrieveSemanticMemoriesViaService(
	ctx context.Context, event *platform.Event, agent Agent, prompt string,
) (string, error) {
	if m == nil || m.semanticRetriever == nil {
		return "", nil
	}
	if agent.SemanticMemory == nil || !agent.SemanticMemory.Enabled {
		return "", nil
	}
	if event == nil || strings.TrimSpace(prompt) == "" {
		return "", nil
	}
	if !m.semanticRetriever.Available(agent.EmbeddingProvider) {
		return "", nil
	}
	req := ai.SemanticRetrievalRequest{
		Scope: ai.SemanticScope{
			TenantID:       event.TenantID,
			Platform:       string(event.Source.Platform),
			ConversationID: event.Conversation.ID,
		},
		Prompt:            prompt,
		EmbeddingProvider: agent.EmbeddingProvider,
		Policy:            agent.SemanticMemory.SemanticRetrievalPolicy,
		CurrentActor:      toSemanticActor(event.Actor),
		RelatedActors:     m.replyChainSemanticActors(ctx, event),
		ReplyRootSummary:  m.replyRootSummary(ctx, event),
	}
	result, err := m.semanticRetriever.Retrieve(ctx, req)
	if err != nil {
		return "", fmt.Errorf("retrieve semantic memories: %w", err)
	}
	return result.Content, nil
}

// toSemanticActor projects one platform actor into the contract type used by
// the SemanticRetriever.
func toSemanticActor(actor platform.Actor) ai.SemanticActorRef {
	name := strings.TrimSpace(actor.DisplayName)
	if name == "" {
		name = strings.TrimSpace(actor.Username)
	}
	return ai.SemanticActorRef{ID: strings.TrimSpace(actor.ID), Name: name, IsBot: actor.IsBot}
}

// replyRootSummary renders a short normalized summary of the reply thread root
// text so the retriever can disambiguate follow-up prompts. Returns an empty
// string when no root is available.
func (m *Module) replyRootSummary(ctx context.Context, event *platform.Event) string {
	if m == nil || m.memory == nil || event == nil {
		return ""
	}
	chain, err := m.memory.GetReplyChain(ctx, event)
	if err != nil || len(chain) == 0 {
		return ""
	}
	root := strings.TrimSpace(chain[0].Article.Text)
	if root == "" {
		return ""
	}
	return trimRunesWithEllipsis(strings.Join(strings.Fields(root), " "), 240)
}

// replyChainSemanticActors collects unique actors participating in the reply
// chain so the retriever can apply actor-weighted ranking.
func (m *Module) replyChainSemanticActors(ctx context.Context, event *platform.Event) []ai.SemanticActorRef {
	if m == nil || m.memory == nil || event == nil {
		return nil
	}
	chain, err := m.memory.GetReplyChain(ctx, event)
	if err != nil {
		return nil
	}
	seen := make(map[string]struct{})
	var actors []ai.SemanticActorRef
	for _, entry := range chain {
		id := strings.TrimSpace(entry.Actor.ID)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		name := strings.TrimSpace(entry.Actor.DisplayName)
		if name == "" {
			name = strings.TrimSpace(entry.Actor.Username)
		}
		actors = append(actors, ai.SemanticActorRef{
			ID:    id,
			Name:  name,
			IsBot: entry.Actor.IsBot,
		})
	}
	return actors
}

package llmchat

import (
	"context"
	"fmt"
	"html"
	"strings"

	"ex-otogi/pkg/otogi/ai"
	"ex-otogi/pkg/otogi/platform"
)

func (m *Module) retrieveSemanticMemories(
	ctx context.Context,
	event *platform.Event,
	agent Agent,
	prompt string,
) (string, error) {
	policy := resolveSemanticMemoryPolicy(agent.SemanticMemory)
	if policy == nil || !policy.Enabled {
		return "", nil
	}
	if event == nil {
		return "", nil
	}
	if strings.TrimSpace(prompt) == "" {
		return "", nil
	}
	if m == nil || m.llmMemory == nil {
		return "", nil
	}

	embeddingProvider, usable, err := m.resolveSemanticMemoryEmbeddingProvider(agent)
	if err != nil {
		return "", fmt.Errorf("retrieve semantic memories resolve embedding provider: %w", err)
	}
	if !usable {
		return "", nil
	}

	scope := semanticMemoryScope(event)
	m.debugSemanticMemoryRetrieve(ctx, scope, prompt)

	queryEmbedding, err := embedSingleText(ctx, embeddingProvider, prompt, ai.EmbeddingTaskTypeQuery)
	if err != nil {
		return "", fmt.Errorf("retrieve semantic memories embed query: %w", err)
	}

	matches, err := m.llmMemory.Search(ctx, ai.LLMMemoryQuery{
		Scope:         scope,
		Embedding:     queryEmbedding,
		Limit:         policy.MaxRetrievedMemories,
		MinSimilarity: policy.MinMemorySimilarity,
	})
	if err != nil {
		return "", fmt.Errorf("retrieve semantic memories search: %w", err)
	}

	serialized := serializeSemanticMemories(matches, policy.MaxMemoryRunes)
	m.debugSemanticMemoryRetrieveResult(ctx, scope, len(matches), len(serialized))

	return serialized, nil
}

func (m *Module) buildSemanticMemoryToolRegistry(
	event *platform.Event,
	agent Agent,
) (*ToolRegistry, error) {
	policy := resolveSemanticMemoryPolicy(agent.SemanticMemory)
	if policy == nil || !policy.Enabled {
		return nil, nil
	}
	if event == nil {
		return nil, nil
	}
	if m == nil || m.llmMemory == nil {
		return nil, nil
	}

	embeddingProvider, usable, err := m.resolveSemanticMemoryEmbeddingProvider(agent)
	if err != nil {
		return nil, fmt.Errorf("build semantic memory tools resolve embedding provider: %w", err)
	}
	if !usable {
		return nil, nil
	}

	return buildSemanticMemoryToolRegistry(
		semanticMemoryScope(event),
		embeddingProvider,
		m.llmMemory,
	), nil
}

func (m *Module) resolveSemanticMemoryEmbeddingProvider(agent Agent) (ai.EmbeddingProvider, bool, error) {
	policy := resolveSemanticMemoryPolicy(agent.SemanticMemory)
	if policy == nil || !policy.Enabled {
		return nil, false, nil
	}
	if strings.TrimSpace(agent.EmbeddingProvider) == "" {
		return nil, false, nil
	}
	if m.embeddingRegistry == nil {
		return nil, false, nil
	}

	provider, err := m.embeddingRegistry.Resolve(agent.EmbeddingProvider)
	if err != nil {
		return nil, false, fmt.Errorf("embedding provider %s: %w", agent.EmbeddingProvider, err)
	}

	return provider, true, nil
}

func semanticMemoryScope(event *platform.Event) ai.LLMMemoryScope {
	if event == nil {
		return ai.LLMMemoryScope{}
	}

	return ai.LLMMemoryScope{
		TenantID:       event.TenantID,
		Platform:       string(event.Source.Platform),
		ConversationID: event.Conversation.ID,
	}
}

func serializeSemanticMemories(matches []ai.LLMMemoryMatch, maxRunes int) string {
	if len(matches) == 0 {
		return ""
	}
	if maxRunes <= 0 {
		maxRunes = defaultMaxMemoryRunes
	}

	renderedMatches := make([]string, 0, len(matches))
	for _, match := range matches {
		renderedMatches = append(renderedMatches, renderSemanticMemoryMatch(match))
	}

	included := make([]string, 0, len(renderedMatches))
	for _, rendered := range renderedMatches {
		candidate := renderSemanticMemoryDocument(append(included, rendered))
		if runeCount(candidate) > maxRunes {
			break
		}
		included = append(included, rendered)
	}
	if len(included) == 0 {
		return ""
	}

	return renderSemanticMemoryDocument(included)
}

func renderSemanticMemoryDocument(renderedMatches []string) string {
	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("<semantic_memories count=\"%d\">\n", len(renderedMatches)))
	for _, rendered := range renderedMatches {
		builder.WriteString(rendered)
		builder.WriteByte('\n')
	}
	builder.WriteString("</semantic_memories>")
	return builder.String()
}

func renderSemanticMemoryMatch(match ai.LLMMemoryMatch) string {
	return fmt.Sprintf(
		"<memory id=\"%s\" category=\"%s\" similarity=\"%.2f\" created_at=\"%s\">\n%s\n</memory>",
		html.EscapeString(match.Record.ID),
		html.EscapeString(match.Record.Category),
		match.Similarity,
		html.EscapeString(match.Record.CreatedAt.UTC().Format(timeLayoutRFC3339)),
		html.EscapeString(match.Record.Content),
	)
}

const timeLayoutRFC3339 = "2006-01-02T15:04:05Z07:00"

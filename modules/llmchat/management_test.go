package llmchat

import (
	"context"
	"strings"
	"testing"
	"time"

	"ex-otogi/pkg/otogi/ai"
)

func TestRetrieveSemanticMemoriesViaServiceReturnsContent(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 17, 12, 0, 0, 0, time.UTC)
	retriever := &semanticRetrieverStub{
		content:    `<semantic_memories count="1"><memory id="mem-1" category="preference">Alice likes tea</memory></semantic_memories>`,
		matchCount: 1,
		available:  true,
	}
	module := newTestModule(validModuleConfig())
	module.semanticRetriever = retriever
	module.clock = func() time.Time { return now }

	agent := module.cfg.Agents[0]
	agent.EmbeddingProvider = "embed-main"
	agent.SemanticMemory = &SemanticMemoryPolicy{
		Enabled: true,
		SemanticRetrievalPolicy: ai.SemanticRetrievalPolicy{
			MaxRetrievedMemories: 3,
			MinSimilarity:        0.4,
			MaxMemoryRunes:       1000,
		},
	}

	ctx := context.Background()
	content, err := module.retrieveSemanticMemoriesViaService(ctx, testLLMChatEvent("Otogi hi"), agent, "hi")
	if err != nil {
		t.Fatalf("retrieveSemanticMemoriesViaService failed: %v", err)
	}
	if !strings.Contains(content, "Alice likes tea") {
		t.Fatalf("content = %q, want substring 'Alice likes tea'", content)
	}
	if retriever.lastReq.EmbeddingProvider != "embed-main" {
		t.Fatalf("embedding_provider = %q, want embed-main", retriever.lastReq.EmbeddingProvider)
	}
}

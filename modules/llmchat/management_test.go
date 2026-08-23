package llmchat

import (
	"context"
	"strings"
	"testing"
	"time"
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
	agent.MemoryEnabled = true

	ctx := context.Background()
	content, err := module.retrieveSemanticMemoriesViaService(ctx, testLLMChatEvent("Otogi hi"), agent, "hi")
	if err != nil {
		t.Fatalf("retrieveSemanticMemoriesViaService failed: %v", err)
	}
	if !strings.Contains(content, "Alice likes tea") {
		t.Fatalf("content = %q, want substring 'Alice likes tea'", content)
	}
}

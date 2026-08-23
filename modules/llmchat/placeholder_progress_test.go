package llmchat

import (
	"testing"

	"ex-otogi/pkg/otogi/ai"
)

func TestPlaceholderProgressAdvancesAcrossSequentialStages(t *testing.T) {
	t.Parallel()

	progress := newPlaceholderProgress()

	thinking := progress.RenderThinking(1, "plan steps")
	if thinking != "Step 1: Thinking\n\nplan steps" {
		t.Fatalf("thinking = %q, want step 1 thinking placeholder", thinking)
	}

	searching := progress.RenderTool(
		ai.LLMToolCall{Name: "web_search", Arguments: `{"query":"Go 1.22 features"}`},
		ai.LLMToolDefinition{Name: "web_search", Description: "Search the web"},
	)
	if searching != "Step 2: Using web search\n\nSearching for \"Go 1.22 features\"" {
		t.Fatalf("searching = %q, want step 2 tool placeholder", searching)
	}

	reading := progress.RenderTool(
		ai.LLMToolCall{Name: "read_url", Arguments: `{"url":"https://example.com/docs","question":"latest changes"}`},
		ai.LLMToolDefinition{Name: "read_url", Description: "Read one URL"},
	)
	if reading != "Step 3: Using read url\n\nReading https://example.com/docs to answer \"latest changes\"" {
		t.Fatalf("reading = %q, want step 3 tool placeholder", reading)
	}

	reviewing := progress.RenderThinking(2, "reviewing tool results")
	if reviewing != "Step 4: Thinking\n\nreviewing tool results" {
		t.Fatalf("reviewing = %q, want step 4 thinking placeholder", reviewing)
	}
}

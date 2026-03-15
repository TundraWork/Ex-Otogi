package ai

import "testing"

func TestLLMAliases(t *testing.T) {
	t.Run("message role validate", func(t *testing.T) {
		if err := LLMMessageRoleTool.Validate(); err != nil {
			t.Fatalf("validate tool role: %v", err)
		}
	})

	t.Run("chunk kind normalize", func(t *testing.T) {
		got := LLMGenerateChunkKindToolCall.Normalize()
		if got != LLMGenerateChunkKindToolCall {
			t.Fatalf("Normalize() = %q, want %q", got, LLMGenerateChunkKindToolCall)
		}
	})
}

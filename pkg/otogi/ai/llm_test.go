package ai

import "testing"

func TestLLMAliases(t *testing.T) {
	t.Run("message role validate", func(t *testing.T) {
		if err := LLMMessageRoleSystem.Validate(); err != nil {
			t.Fatalf("validate system role: %v", err)
		}
	})

	t.Run("chunk kind normalize", func(t *testing.T) {
		got := LLMGenerateChunkKind("").Normalize()
		if got != LLMGenerateChunkKindOutputText {
			t.Fatalf("Normalize() = %q, want %q", got, LLMGenerateChunkKindOutputText)
		}
	})
}

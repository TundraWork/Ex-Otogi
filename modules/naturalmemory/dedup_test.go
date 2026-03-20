package naturalmemory

import (
	"testing"
	"time"

	"ex-otogi/pkg/otogi/ai"
)

func TestClassifyCandidate(t *testing.T) {
	t.Parallel()

	now := time.Unix(100, 0).UTC()
	tests := []struct {
		name       string
		candidate  extractedMemory
		matches    []ai.LLMMemoryMatch
		threshold  float32
		wantAction dedupAction
		wantID     string
	}{
		{
			name:       "no matches adds",
			candidate:  extractedMemory{Content: "Alice likes tea"},
			threshold:  0.85,
			wantAction: dedupActionAdd,
		},
		{
			name:      "same content noops",
			candidate: extractedMemory{Content: "Alice likes tea"},
			threshold: 0.85,
			matches: []ai.LLMMemoryMatch{{
				Record:     ai.LLMMemoryRecord{ID: "mem-1", Content: "Alice likes tea", UpdatedAt: now},
				Similarity: 0.92,
			}},
			wantAction: dedupActionNoop,
		},
		{
			name:      "expanded fact updates",
			candidate: extractedMemory{Content: "Alice likes jasmine tea"},
			threshold: 0.85,
			matches: []ai.LLMMemoryMatch{{
				Record:     ai.LLMMemoryRecord{ID: "mem-2", Content: "Alice likes tea", UpdatedAt: now},
				Similarity: 0.9,
			}},
			wantAction: dedupActionUpdate,
			wantID:     "mem-2",
		},
		{
			name:      "best qualifying match wins",
			candidate: extractedMemory{Content: "Alice lives in Sydney now"},
			threshold: 0.85,
			matches: []ai.LLMMemoryMatch{
				{
					Record:     ai.LLMMemoryRecord{ID: "mem-3", Content: "Alice lives in Melbourne"},
					Similarity: 0.86,
				},
				{
					Record:     ai.LLMMemoryRecord{ID: "mem-4", Content: "Alice lives in Brisbane"},
					Similarity: 0.95,
				},
			},
			wantAction: dedupActionUpdate,
			wantID:     "mem-4",
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			gotAction, gotID := classifyCandidate(testCase.candidate, []float32{1, 0}, testCase.matches, testCase.threshold)
			if gotAction != testCase.wantAction {
				t.Fatalf("action = %q, want %q", gotAction, testCase.wantAction)
			}
			if gotID != testCase.wantID {
				t.Fatalf("updateTargetID = %q, want %q", gotID, testCase.wantID)
			}
		})
	}
}

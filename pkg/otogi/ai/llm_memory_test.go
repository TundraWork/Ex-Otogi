package ai

import (
	"strings"
	"testing"
	"time"
)

func TestLLMMemoryUpdateValidate(t *testing.T) {
	t.Parallel()

	valid := LLMMemoryUpdate{
		ID:        "mem-1",
		Content:   "Alice likes tea",
		Category:  "preference",
		Embedding: []float32{1, 0},
		Profile: LLMMemoryProfile{
			Kind:           LLMMemoryKindUnit,
			Importance:     7,
			LastAccessedAt: time.Unix(100, 0).UTC(),
			AccessCount:    2,
			Source:         "natural",
			SourceActor: &LLMMemoryActorRef{
				ID:   "user-1",
				Name: "Alice",
			},
		},
	}

	testCases := []struct {
		name    string
		mutate  func(*LLMMemoryUpdate)
		wantErr string
	}{
		{
			name:   "valid",
			mutate: func(*LLMMemoryUpdate) {},
		},
		{
			name: "missing id",
			mutate: func(update *LLMMemoryUpdate) {
				update.ID = ""
			},
			wantErr: "missing id",
		},
		{
			name: "invalid kind",
			mutate: func(update *LLMMemoryUpdate) {
				update.Profile.Kind = "mystery"
			},
			wantErr: "unsupported kind",
		},
		{
			name: "negative access count",
			mutate: func(update *LLMMemoryUpdate) {
				update.Profile.AccessCount = -1
			},
			wantErr: "access_count must be >= 0",
		},
	}

	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			update := valid
			update.Profile = valid.Profile
			testCase.mutate(&update)

			err := update.Validate()
			if testCase.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate failed: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate error = nil, want %q", testCase.wantErr)
			}
			if !strings.Contains(err.Error(), testCase.wantErr) {
				t.Fatalf("Validate error = %q, want substring %q", err, testCase.wantErr)
			}
		})
	}
}

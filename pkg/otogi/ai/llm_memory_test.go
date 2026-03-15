package ai

import "testing"

func TestLLMMemoryScopeValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		scope   LLMMemoryScope
		wantErr bool
	}{
		{
			name: "valid scope",
			scope: LLMMemoryScope{
				Platform:       "telegram",
				ConversationID: "chat-1",
			},
		},
		{
			name: "missing platform",
			scope: LLMMemoryScope{
				ConversationID: "chat-1",
			},
			wantErr: true,
		},
		{
			name: "missing conversation id",
			scope: LLMMemoryScope{
				Platform: "telegram",
			},
			wantErr: true,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			err := testCase.scope.Validate()
			if testCase.wantErr && err == nil {
				t.Fatal("expected error")
			}
			if !testCase.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestLLMMemoryEntryValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		entry   LLMMemoryEntry
		wantErr bool
	}{
		{
			name: "valid entry",
			entry: LLMMemoryEntry{
				Scope: LLMMemoryScope{
					Platform:       "telegram",
					ConversationID: "chat-1",
				},
				Content:   "User prefers tea.",
				Category:  "preference",
				Embedding: []float32{1, 0},
			},
		},
		{
			name: "missing content",
			entry: LLMMemoryEntry{
				Scope: LLMMemoryScope{
					Platform:       "telegram",
					ConversationID: "chat-1",
				},
				Category:  "preference",
				Embedding: []float32{1, 0},
			},
			wantErr: true,
		},
		{
			name: "missing category",
			entry: LLMMemoryEntry{
				Scope: LLMMemoryScope{
					Platform:       "telegram",
					ConversationID: "chat-1",
				},
				Content:   "User prefers tea.",
				Embedding: []float32{1, 0},
			},
			wantErr: true,
		},
		{
			name: "missing embedding",
			entry: LLMMemoryEntry{
				Scope: LLMMemoryScope{
					Platform:       "telegram",
					ConversationID: "chat-1",
				},
				Content:  "User prefers tea.",
				Category: "preference",
			},
			wantErr: true,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			err := testCase.entry.Validate()
			if testCase.wantErr && err == nil {
				t.Fatal("expected error")
			}
			if !testCase.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestLLMMemoryQueryValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		query   LLMMemoryQuery
		wantErr bool
	}{
		{
			name: "valid query",
			query: LLMMemoryQuery{
				Scope: LLMMemoryScope{
					Platform:       "telegram",
					ConversationID: "chat-1",
				},
				Embedding:     []float32{1, 0},
				Limit:         5,
				MinSimilarity: 0.5,
			},
		},
		{
			name: "negative limit",
			query: LLMMemoryQuery{
				Scope: LLMMemoryScope{
					Platform:       "telegram",
					ConversationID: "chat-1",
				},
				Embedding: []float32{1, 0},
				Limit:     -1,
			},
			wantErr: true,
		},
		{
			name: "similarity above one",
			query: LLMMemoryQuery{
				Scope: LLMMemoryScope{
					Platform:       "telegram",
					ConversationID: "chat-1",
				},
				Embedding:     []float32{1, 0},
				MinSimilarity: 1.1,
			},
			wantErr: true,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			err := testCase.query.Validate()
			if testCase.wantErr && err == nil {
				t.Fatal("expected error")
			}
			if !testCase.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

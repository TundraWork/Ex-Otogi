package naturalmemory

import (
	"strings"
	"testing"
	"time"

	"ex-otogi/pkg/otogi/ai"
	"ex-otogi/pkg/otogi/core"
	"ex-otogi/pkg/otogi/platform"
)

func TestParseExtractionResponse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     string
		wantCount int
		wantErr   bool
	}{
		{
			name:      "plain json",
			input:     `[{"content":"Alice likes tea","category":"preference","importance":7,"subject_actor_id":"user-1","subject_actor_name":"Alice"}]`,
			wantCount: 1,
		},
		{
			name: "markdown fence",
			input: "```json\n" +
				`[{"content":"Alice is a student","category":"user_fact","importance":8}]` +
				"\n```",
			wantCount: 1,
		},
		{
			name:      "json embedded in prose",
			input:     `Memories: [{"content":"Trip booked","category":"experience","importance":6}] end.`,
			wantCount: 1,
		},
		{
			name: "filters invalid entries",
			input: `[
				{"content":"  ","category":"preference","importance":7},
				{"content":"Valid fact","category":"knowledge","importance":5},
				{"content":"Bad category","category":"misc","importance":8},
				{"content":"Bad importance","category":"knowledge","importance":11}
			]`,
			wantCount: 1,
		},
		{
			name:    "invalid json",
			input:   `{not-json}`,
			wantErr: true,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseExtractionResponse(testCase.input)
			if testCase.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseExtractionResponse failed: %v", err)
			}
			if len(got) != testCase.wantCount {
				t.Fatalf("candidate count = %d, want %d", len(got), testCase.wantCount)
			}
		})
	}
}

func TestRenderExtractionPrompt(t *testing.T) {
	t.Parallel()

	prompt := renderExtractionPrompt(extractionContext{
		ConversationText: "alice: I like jasmine tea",
		AnchorTime:       time.Date(2026, time.March, 16, 12, 0, 0, 0, time.UTC),
		Participants: []ai.LLMMemoryActorRef{
			{ID: "user-1", Name: "Alice"},
		},
	}, []ai.LLMMemoryRecord{
		{ID: "mem-1", Category: "preference", Content: "Alice likes tea"},
		{ID: "mem-2", Category: "knowledge", Content: "Alice studies chemistry"},
	})

	if !strings.Contains(prompt, "<anchor_time>") {
		t.Fatalf("prompt = %q, want anchor time block", prompt)
	}
	if !strings.Contains(prompt, "<participants>") {
		t.Fatalf("prompt = %q, want participants block", prompt)
	}
	if !strings.Contains(prompt, "<existing_memories>") {
		t.Fatalf("prompt = %q, want existing memories block", prompt)
	}
	if !strings.Contains(prompt, `Alice likes tea`) {
		t.Fatalf("prompt = %q, want first memory content", prompt)
	}
	if !strings.Contains(prompt, "<conversation>\nalice: I like jasmine tea\n</conversation>") {
		t.Fatalf("prompt = %q, want conversation block", prompt)
	}
	if !strings.Contains(prompt, `"subject_actor_id":"..."`) {
		t.Fatalf("prompt = %q, want subject actor output instructions", prompt)
	}
	if !strings.Contains(prompt, `"category":"user_fact|preference|knowledge|experience"`) {
		t.Fatalf("prompt = %q, want json output instructions", prompt)
	}
}

func TestSerializeExtractionConversation(t *testing.T) {
	t.Parallel()

	serialized := serializeExtractionConversation([]core.ConversationContextEntry{
		{
			Actor:   platform.Actor{ID: "u-1", Username: "alice"},
			Article: platform.Article{ID: "a-1", Text: "I like tea"},
		},
		{
			Actor:   platform.Actor{ID: "bot-1", Username: "otogi", IsBot: true},
			Article: platform.Article{ID: "a-2", Text: "Noted that."},
		},
	}, extractionConversationEntry{
		Actor:   platform.Actor{ID: "u-2", DisplayName: "Bob"},
		Article: platform.Article{ID: "a-3", Text: "Also I moved to Sydney"},
	}, 90)

	if !strings.Contains(serialized, "Bob <actor:u-2>: Also I moved to Sydney") {
		t.Fatalf("serialized = %q, want current article line", serialized)
	}
	if !strings.Contains(serialized, "otogi <actor:bot-1> (bot): Noted that.") {
		t.Fatalf("serialized = %q, want bot line", serialized)
	}
	if strings.Contains(serialized, "alice <actor:u-1>: I like tea") {
		t.Fatalf("serialized = %q, did not expect oldest line after trimming", serialized)
	}
}

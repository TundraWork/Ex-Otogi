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

func TestParseExtractionResponseWithValidUntil(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		input          string
		wantCount      int
		wantValidUntil string
	}{
		{
			name:           "valid_until present",
			input:          `[{"content":"Alice is visiting Tokyo until April","category":"experience","importance":6,"valid_until":"2026-04-01T00:00:00Z"}]`,
			wantCount:      1,
			wantValidUntil: "2026-04-01T00:00:00Z",
		},
		{
			name:           "valid_until empty",
			input:          `[{"content":"Alice likes tea","category":"preference","importance":7,"valid_until":""}]`,
			wantCount:      1,
			wantValidUntil: "",
		},
		{
			name:           "valid_until missing",
			input:          `[{"content":"Alice likes tea","category":"preference","importance":7}]`,
			wantCount:      1,
			wantValidUntil: "",
		},
		{
			name:           "valid_until invalid format stripped",
			input:          `[{"content":"Alice likes tea","category":"preference","importance":7,"valid_until":"next week"}]`,
			wantCount:      1,
			wantValidUntil: "",
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseExtractionResponse(testCase.input)
			if err != nil {
				t.Fatalf("parseExtractionResponse failed: %v", err)
			}
			if len(got) != testCase.wantCount {
				t.Fatalf("candidate count = %d, want %d", len(got), testCase.wantCount)
			}
			if len(got) > 0 && got[0].ValidUntil != testCase.wantValidUntil {
				t.Fatalf("valid_until = %q, want %q", got[0].ValidUntil, testCase.wantValidUntil)
			}
		})
	}
}

func TestParseExtractionResponseWithKeywordsAndTags(t *testing.T) {
	t.Parallel()

	input := `[{"content":"Alice likes jasmine tea","category":"preference","importance":7,"keywords":["tea","jasmine","preference"],"tags":["beverage"]}]`
	got, err := parseExtractionResponse(input)
	if err != nil {
		t.Fatalf("parseExtractionResponse failed: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("candidate count = %d, want 1", len(got))
	}
	if len(got[0].Keywords) != 3 || got[0].Keywords[0] != "tea" {
		t.Fatalf("keywords = %v, want [tea jasmine preference]", got[0].Keywords)
	}
	if len(got[0].Tags) != 1 || got[0].Tags[0] != "beverage" {
		t.Fatalf("tags = %v, want [beverage]", got[0].Tags)
	}
}

func TestBuildEmbeddingTextIncludesKeywordsAndTags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		memory   extractedMemory
		wantText string
	}{
		{
			name:     "content only",
			memory:   extractedMemory{Content: "Alice likes tea"},
			wantText: "Alice likes tea",
		},
		{
			name: "with keywords and tags",
			memory: extractedMemory{
				Content:  "Alice likes tea",
				Keywords: []string{"tea", "jasmine"},
				Tags:     []string{"beverage"},
			},
			wantText: "Alice likes tea tea jasmine beverage",
		},
		{
			name: "empty keywords ignored",
			memory: extractedMemory{
				Content:  "Alice likes tea",
				Keywords: []string{"tea", "  ", ""},
				Tags:     []string{},
			},
			wantText: "Alice likes tea tea",
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got := buildEmbeddingText(testCase.memory)
			if got != testCase.wantText {
				t.Fatalf("buildEmbeddingText = %q, want %q", got, testCase.wantText)
			}
		})
	}
}

func TestGenerateLinksFromMatches(t *testing.T) {
	t.Parallel()

	matches := []ai.LLMMemoryMatch{
		{Record: ai.LLMMemoryRecord{ID: "mem-1"}, Similarity: 0.9},
		{Record: ai.LLMMemoryRecord{ID: "mem-2"}, Similarity: 0.7},
		{Record: ai.LLMMemoryRecord{ID: "mem-3"}, Similarity: 0.3},
	}

	tests := []struct {
		name         string
		action       synthesisAction
		targetID     string
		absorbedIDs  []string
		threshold    float32
		maxLinks     int
		wantCount    int
		wantRelation string
	}{
		{
			name:         "add creates related links above threshold",
			action:       synthesisActionAdd,
			threshold:    0.5,
			maxLinks:     3,
			wantCount:    2,
			wantRelation: "related",
		},
		{
			name:         "rewrite creates refines links",
			action:       synthesisActionRewrite,
			targetID:     "mem-1",
			threshold:    0.5,
			maxLinks:     3,
			wantCount:    1,
			wantRelation: "refines",
		},
		{
			name:         "supersede excludes deleted target",
			action:       synthesisActionSupersede,
			targetID:     "mem-1",
			absorbedIDs:  []string{"mem-2"},
			threshold:    0.1,
			maxLinks:     3,
			wantCount:    1,
			wantRelation: "related",
		},
		{
			name:      "max links caps output",
			action:    synthesisActionAdd,
			threshold: 0.1,
			maxLinks:  1,
			wantCount: 1,
		},
		{
			name:      "high threshold filters all",
			action:    synthesisActionAdd,
			threshold: 0.95,
			maxLinks:  3,
			wantCount: 0,
		},
		{
			name:      "empty matches returns nil",
			action:    synthesisActionAdd,
			threshold: 0.1,
			maxLinks:  3,
			wantCount: 0,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			input := matches
			if testCase.name == "empty matches returns nil" {
				input = nil
			}
			links := generateLinks(input, testCase.action, testCase.targetID, testCase.absorbedIDs, testCase.threshold, testCase.maxLinks)
			if len(links) != testCase.wantCount {
				t.Fatalf("links len = %d, want %d", len(links), testCase.wantCount)
			}
			if testCase.wantRelation != "" && len(links) > 0 && links[0].Relation != testCase.wantRelation {
				t.Fatalf("links[0].relation = %q, want %q", links[0].Relation, testCase.wantRelation)
			}
		})
	}
}

func TestMergeLinksDeduplicates(t *testing.T) {
	t.Parallel()

	existing := []ai.LLMMemoryLink{
		{TargetID: "mem-1", Relation: "related"},
		{TargetID: "mem-2", Relation: "refines"},
	}
	additions := []ai.LLMMemoryLink{
		{TargetID: "mem-2", Relation: "related"},
		{TargetID: "mem-3", Relation: "related"},
	}

	merged := mergeLinks(existing, additions)
	if len(merged) != 3 {
		t.Fatalf("merged len = %d, want 3", len(merged))
	}
	// mem-2 should keep the existing relation (refines), not be overwritten.
	for _, link := range merged {
		if link.TargetID == "mem-2" && link.Relation != "refines" {
			t.Fatalf("mem-2 relation = %q, want refines (existing preserved)", link.Relation)
		}
	}
}

func TestBuildMemoryProfileWithValidUntil(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 16, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		validUntil string
		wantNil    bool
	}{
		{
			name:       "with valid_until",
			validUntil: "2026-06-01T00:00:00Z",
			wantNil:    false,
		},
		{
			name:       "empty valid_until",
			validUntil: "",
			wantNil:    true,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			candidate := extractedMemory{
				Content:    "Some fact",
				Category:   "knowledge",
				Importance: 5,
				ValidUntil: testCase.validUntil,
			}
			profile := buildMemoryProfile(candidate, extractionContext{AnchorTime: now}, now)
			if testCase.wantNil && profile.ValidUntil != nil {
				t.Fatalf("profile.ValidUntil = %v, want nil", profile.ValidUntil)
			}
			if !testCase.wantNil && profile.ValidUntil == nil {
				t.Fatal("profile.ValidUntil = nil, want non-nil")
			}
			if !testCase.wantNil {
				expected, err := time.Parse(time.RFC3339, testCase.validUntil)
				if err != nil {
					t.Fatalf("parse expected valid_until: %v", err)
				}
				if !profile.ValidUntil.Equal(expected) {
					t.Fatalf("profile.ValidUntil = %s, want %s", profile.ValidUntil, expected)
				}
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
	if !strings.Contains(prompt, `"valid_until":""`) {
		t.Fatalf("prompt = %q, want valid_until in output format", prompt)
	}
	if !strings.Contains(prompt, "time-bounded") {
		t.Fatalf("prompt = %q, want time-bounded extraction criteria", prompt)
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

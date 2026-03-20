package discord

import (
	"testing"

	"ex-otogi/pkg/otogi/platform"
)

func TestRenderDiscordMarkdown(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		text     string
		entities []platform.TextEntity
		want     string
	}{
		{
			name: "empty text",
			text: "",
			want: "",
		},
		{
			name: "no entities passthrough",
			text: "hello world",
			want: "hello world",
		},
		{
			name: "bold",
			text: "hello world",
			entities: []platform.TextEntity{
				{Type: platform.TextEntityTypeBold, Offset: 0, Length: 5},
			},
			want: "**hello** world",
		},
		{
			name: "italic",
			text: "hello world",
			entities: []platform.TextEntity{
				{Type: platform.TextEntityTypeItalic, Offset: 6, Length: 5},
			},
			want: "hello _world_",
		},
		{
			name: "underline",
			text: "hello",
			entities: []platform.TextEntity{
				{Type: platform.TextEntityTypeUnderline, Offset: 0, Length: 5},
			},
			want: "__hello__",
		},
		{
			name: "strikethrough",
			text: "hello",
			entities: []platform.TextEntity{
				{Type: platform.TextEntityTypeStrike, Offset: 0, Length: 5},
			},
			want: "~~hello~~",
		},
		{
			name: "spoiler",
			text: "secret",
			entities: []platform.TextEntity{
				{Type: platform.TextEntityTypeSpoiler, Offset: 0, Length: 6},
			},
			want: "||secret||",
		},
		{
			name: "inline code",
			text: "use fmt.Println()",
			entities: []platform.TextEntity{
				{Type: platform.TextEntityTypeCode, Offset: 4, Length: 13},
			},
			want: "use `fmt.Println()`",
		},
		{
			name: "code block without language",
			text: "var x = 1",
			entities: []platform.TextEntity{
				{Type: platform.TextEntityTypePre, Offset: 0, Length: 9},
			},
			want: "```\nvar x = 1\n```",
		},
		{
			name: "code block with language",
			text: "func main() {}",
			entities: []platform.TextEntity{
				{Type: platform.TextEntityTypePre, Offset: 0, Length: 14, Language: "go"},
			},
			want: "```go\nfunc main() {}\n```",
		},
		{
			name: "text url",
			text: "click here",
			entities: []platform.TextEntity{
				{Type: platform.TextEntityTypeTextURL, Offset: 0, Length: 10, URL: "https://example.com"},
			},
			want: "[click here](https://example.com)",
		},
		{
			name: "mention name replaces span",
			text: "Hello Alice",
			entities: []platform.TextEntity{
				{Type: platform.TextEntityTypeMentionName, Offset: 6, Length: 5, MentionUserID: "12345"},
			},
			want: "Hello <@12345>",
		},
		{
			name: "nested bold italic",
			text: "bold italic rest",
			entities: []platform.TextEntity{
				{Type: platform.TextEntityTypeBold, Offset: 0, Length: 11},
				{Type: platform.TextEntityTypeItalic, Offset: 5, Length: 6},
			},
			want: "**bold _italic_** rest",
		},
		{
			name: "unknown entity type passthrough",
			text: "hello",
			entities: []platform.TextEntity{
				{Type: platform.TextEntityTypeURL, Offset: 0, Length: 5},
			},
			want: "hello",
		},
		{
			name: "entity out of range ignored",
			text: "hi",
			entities: []platform.TextEntity{
				{Type: platform.TextEntityTypeBold, Offset: 10, Length: 5},
			},
			want: "hi",
		},
		{
			name: "multiple separate entities",
			text: "bold and italic",
			entities: []platform.TextEntity{
				{Type: platform.TextEntityTypeBold, Offset: 0, Length: 4},
				{Type: platform.TextEntityTypeItalic, Offset: 9, Length: 6},
			},
			want: "**bold** and _italic_",
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got := renderDiscordMarkdown(testCase.text, testCase.entities)
			if got != testCase.want {
				t.Errorf("renderDiscordMarkdown(%q, entities) =\n  %q\nwant:\n  %q",
					testCase.text, got, testCase.want)
			}
		})
	}
}

func TestSplitDiscordMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		text      string
		wantParts int
		wantFirst string
	}{
		{
			name:      "short message not split",
			text:      "hello world",
			wantParts: 1,
			wantFirst: "hello world",
		},
		{
			name:      "exact limit not split",
			text:      repeatStr("x", discordMaxMessageLength),
			wantParts: 1,
		},
		{
			name:      "exceeds limit is split",
			text:      repeatStr("x", discordMaxMessageLength+1),
			wantParts: 2,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			parts := splitDiscordMessage(testCase.text)
			if len(parts) != testCase.wantParts {
				t.Errorf("splitDiscordMessage: got %d parts, want %d", len(parts), testCase.wantParts)
			}
			if testCase.wantFirst != "" && len(parts) > 0 && parts[0] != testCase.wantFirst {
				t.Errorf("splitDiscordMessage parts[0] = %q, want %q", parts[0], testCase.wantFirst)
			}
		})
	}
}

func TestTruncateDiscordEdit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		text       string
		wantTrunc  bool
		wantSuffix bool
	}{
		{
			name:       "short text unchanged",
			text:       "hello",
			wantTrunc:  false,
			wantSuffix: false,
		},
		{
			name:       "exact limit unchanged",
			text:       repeatStr("x", discordMaxMessageLength),
			wantTrunc:  false,
			wantSuffix: false,
		},
		{
			name:       "over limit is truncated",
			text:       repeatStr("x", discordMaxMessageLength+1),
			wantTrunc:  true,
			wantSuffix: true,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			result := truncateDiscordEdit(testCase.text)
			runeLen := len([]rune(result))
			if runeLen > discordMaxMessageLength {
				t.Errorf("result rune length %d exceeds %d", runeLen, discordMaxMessageLength)
			}
			if testCase.wantSuffix {
				if !containsStr(result, discordTruncationSuffix) {
					t.Errorf("truncated result missing suffix %q: %q", discordTruncationSuffix, result)
				}
			}
		})
	}
}

func repeatStr(s string, n int) string {
	b := make([]byte, len(s)*n)
	for i := 0; i < n; i++ {
		copy(b[i*len(s):], s)
	}

	return string(b)
}

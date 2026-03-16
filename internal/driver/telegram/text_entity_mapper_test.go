package telegram

import (
	"testing"

	"ex-otogi/pkg/otogi/platform"

	"github.com/gotd/td/tg"
)

func TestMapTextEntities(t *testing.T) {
	t.Parallel()

	// All ASCII text: UTF-16 offsets == rune offsets.
	text := "bold pre! hello world e blockquote here!"
	entities := []tg.MessageEntityClass{
		&tg.MessageEntityBold{Offset: 0, Length: 4},
		&tg.MessageEntityPre{Offset: 5, Length: 3, Language: "go"},
		&tg.MessageEntityTextURL{Offset: 9, Length: 5, URL: "https://example.com"},
		&tg.MessageEntityMentionName{Offset: 15, Length: 5, UserID: 123},
		&tg.MessageEntityCustomEmoji{Offset: 21, Length: 1, DocumentID: 999},
		&tg.MessageEntityBlockquote{Collapsed: true, Offset: 23, Length: 7},
	}

	got := mapTextEntities(text, entities)
	if len(got) != len(entities) {
		t.Fatalf("entity len = %d, want %d", len(got), len(entities))
	}

	if got[0].Type != platform.TextEntityTypeBold {
		t.Fatalf("entity[0].Type = %q, want %q", got[0].Type, platform.TextEntityTypeBold)
	}
	if got[1].Type != platform.TextEntityTypePre || got[1].Language != "go" {
		t.Fatalf("entity[1] = %+v, want pre with language go", got[1])
	}
	if got[2].Type != platform.TextEntityTypeTextURL || got[2].URL != "https://example.com" {
		t.Fatalf("entity[2] = %+v, want text_url with URL", got[2])
	}
	if got[3].Type != platform.TextEntityTypeMentionName || got[3].MentionUserID != "123" {
		t.Fatalf("entity[3] = %+v, want mention_name with user id", got[3])
	}
	if got[4].Type != platform.TextEntityTypeCustomEmoji || got[4].CustomEmojiID != "999" {
		t.Fatalf("entity[4] = %+v, want custom_emoji with id", got[4])
	}
	if got[5].Type != platform.TextEntityTypeBlockquote || !got[5].Collapsed {
		t.Fatalf("entity[5] = %+v, want collapsed blockquote", got[5])
	}
}

func TestMapTextEntitiesUTF16Conversion(t *testing.T) {
	t.Parallel()

	// "a😀b" — the emoji U+1F600 occupies 2 UTF-16 code units (surrogate pair).
	// UTF-16 offsets: a=0, 😀=1..2, b=3
	// Rune   indices: a=0, 😀=1,    b=2
	text := "a😀b"
	entities := []tg.MessageEntityClass{
		// Bold on the emoji: UTF-16 offset=1, length=2
		&tg.MessageEntityBold{Offset: 1, Length: 2},
		// Italic on 'b': UTF-16 offset=3, length=1
		&tg.MessageEntityItalic{Offset: 3, Length: 1},
	}

	got := mapTextEntities(text, entities)
	if len(got) != 2 {
		t.Fatalf("entity len = %d, want 2", len(got))
	}

	// Emoji should map to rune offset=1, length=1.
	if got[0].Offset != 1 || got[0].Length != 1 {
		t.Fatalf("entity[0] offset=%d length=%d, want offset=1 length=1", got[0].Offset, got[0].Length)
	}
	// 'b' should map to rune offset=2, length=1.
	if got[1].Offset != 2 || got[1].Length != 1 {
		t.Fatalf("entity[1] offset=%d length=%d, want offset=2 length=1", got[1].Offset, got[1].Length)
	}
}

func TestMapTextEntitiesCustomEmojiUTF16(t *testing.T) {
	t.Parallel()

	// Simulates the exact error scenario: text with 21 runes where a custom
	// emoji at the end is a supplementary-plane character (2 UTF-16 units).
	// Telegram sends offset=21, length=2 in UTF-16 units.
	// "abcdefghijklmnopqrstu" is 21 ASCII chars, then one emoji.
	text := "abcdefghijklmnopqrstu\U0001F525" // 22 runes, 23 UTF-16 units
	entities := []tg.MessageEntityClass{
		&tg.MessageEntityCustomEmoji{Offset: 21, Length: 2, DocumentID: 42},
	}

	got := mapTextEntities(text, entities)
	if len(got) != 1 {
		t.Fatalf("entity len = %d, want 1", len(got))
	}

	// Should convert to rune offset=21, length=1 (one emoji rune).
	if got[0].Offset != 21 || got[0].Length != 1 {
		t.Fatalf("entity[0] offset=%d length=%d, want offset=21 length=1", got[0].Offset, got[0].Length)
	}
	if got[0].CustomEmojiID != "42" {
		t.Fatalf("entity[0].CustomEmojiID = %q, want %q", got[0].CustomEmojiID, "42")
	}

	// Validate that the converted entities pass validation.
	err := platform.ValidateTextEntities(text, got)
	if err != nil {
		t.Fatalf("ValidateTextEntities failed after conversion: %v", err)
	}
}

func TestBuildRuneFromUTF16(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		text string
		want []int
	}{
		{
			name: "ascii only",
			text: "abc",
			// UTF-16 offsets 0,1,2 map to rune indices 0,1,2; sentinel=3
			want: []int{0, 1, 2, 3},
		},
		{
			name: "single emoji",
			text: "😀",
			// 😀 = U+1F600 takes 2 UTF-16 code units
			// UTF-16 offset 0 -> rune 0, offset 1 -> rune 0, sentinel=1
			want: []int{0, 0, 1},
		},
		{
			name: "mixed ascii and emoji",
			text: "a😀b",
			// a: utf16=0 -> rune 0
			// 😀: utf16=1 -> rune 1, utf16=2 -> rune 1
			// b: utf16=3 -> rune 2
			// sentinel: rune 3
			want: []int{0, 1, 1, 2, 3},
		},
		{
			name: "empty string",
			text: "",
			want: []int{0},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := buildRuneFromUTF16(tt.text)
			if len(got) != len(tt.want) {
				t.Fatalf("len = %d, want %d; got %v", len(got), len(tt.want), got)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("mapping[%d] = %d, want %d; full: %v", i, got[i], tt.want[i], got)
				}
			}
		})
	}
}

func TestMapTextEntityTypeFromTelegramUnknownFallback(t *testing.T) {
	t.Parallel()

	got := mapTextEntityTypeFromTelegram(nil)
	if got != platform.TextEntityTypeUnknown {
		t.Fatalf("type = %q, want %q", got, platform.TextEntityTypeUnknown)
	}
}

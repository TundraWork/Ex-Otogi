package telegram

import (
	"testing"

	"ex-otogi/pkg/otogi"

	"github.com/gotd/td/tg"
)

func TestMapTextEntities(t *testing.T) {
	t.Parallel()

	entities := []tg.MessageEntityClass{
		&tg.MessageEntityBold{Offset: 0, Length: 4},
		&tg.MessageEntityPre{Offset: 5, Length: 3, Language: "go"},
		&tg.MessageEntityTextURL{Offset: 9, Length: 5, URL: "https://example.com"},
		&tg.MessageEntityMentionName{Offset: 15, Length: 5, UserID: 123},
		&tg.MessageEntityCustomEmoji{Offset: 21, Length: 1, DocumentID: 999},
		&tg.MessageEntityBlockquote{Collapsed: true, Offset: 23, Length: 7},
	}

	got := mapTextEntities(entities)
	if len(got) != len(entities) {
		t.Fatalf("entity len = %d, want %d", len(got), len(entities))
	}

	if got[0].Type != otogi.TextEntityTypeBold {
		t.Fatalf("entity[0].Type = %q, want %q", got[0].Type, otogi.TextEntityTypeBold)
	}
	if got[1].Type != otogi.TextEntityTypePre || got[1].Language != "go" {
		t.Fatalf("entity[1] = %+v, want pre with language go", got[1])
	}
	if got[2].Type != otogi.TextEntityTypeTextURL || got[2].URL != "https://example.com" {
		t.Fatalf("entity[2] = %+v, want text_url with URL", got[2])
	}
	if got[3].Type != otogi.TextEntityTypeMentionName || got[3].MentionUserID != "123" {
		t.Fatalf("entity[3] = %+v, want mention_name with user id", got[3])
	}
	if got[4].Type != otogi.TextEntityTypeCustomEmoji || got[4].CustomEmojiID != "999" {
		t.Fatalf("entity[4] = %+v, want custom_emoji with id", got[4])
	}
	if got[5].Type != otogi.TextEntityTypeBlockquote || !got[5].Collapsed {
		t.Fatalf("entity[5] = %+v, want collapsed blockquote", got[5])
	}
}

func TestMapTextEntityTypeFromTelegramUnknownFallback(t *testing.T) {
	t.Parallel()

	got := mapTextEntityTypeFromTelegram(nil)
	if got != otogi.TextEntityTypeUnknown {
		t.Fatalf("type = %q, want %q", got, otogi.TextEntityTypeUnknown)
	}
}

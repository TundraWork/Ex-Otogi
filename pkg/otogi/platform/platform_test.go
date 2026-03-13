package platform

import (
	"strings"
	"testing"
	"time"
)

func TestPlatformHelpers(t *testing.T) {
	t.Run("parse command candidate", func(t *testing.T) {
		candidate, matched, err := ParseCommandCandidate("/hello world")
		if err != nil {
			t.Fatalf("ParseCommandCandidate() error = %v", err)
		}
		if !matched {
			t.Fatal("ParseCommandCandidate() matched = false, want true")
		}
		if candidate.Name != "hello" {
			t.Fatalf("candidate.Name = %q, want %q", candidate.Name, "hello")
		}
	})

	t.Run("validate text entities rejects invalid ranges", func(t *testing.T) {
		err := ValidateTextEntities("abc", []TextEntity{{
			Type:   TextEntityTypeBold,
			Offset: 2,
			Length: 2,
		}})
		if err == nil {
			t.Fatal("ValidateTextEntities() error = nil, want non-nil")
		}
		if !strings.Contains(err.Error(), "exceeds text length") {
			t.Fatalf("ValidateTextEntities() error = %q, want entity range detail", err)
		}
	})

	t.Run("media download request from event", func(t *testing.T) {
		event := &Event{
			Kind: EventKindArticleCreated,
			Source: EventSource{
				Platform: PlatformTelegram,
				ID:       "telegram-primary",
			},
			Conversation: Conversation{
				ID:   "chat-1",
				Type: ConversationTypeGroup,
			},
			Article: &Article{
				ID: "article-1",
			},
			OccurredAt: time.Now().UTC(),
		}

		request, err := MediaDownloadRequestFromEvent(event, "attachment-1")
		if err != nil {
			t.Fatalf("MediaDownloadRequestFromEvent() error = %v", err)
		}
		if request.ArticleID != "article-1" {
			t.Fatalf("request.ArticleID = %q, want %q", request.ArticleID, "article-1")
		}
	})
}

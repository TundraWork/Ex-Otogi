package platform

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNewInvalidEventDetailErrorUnwrapsSentinelAndCause(t *testing.T) {
	t.Parallel()

	cause := errors.New("bad entities")
	err := newInvalidEventDetailError("article.created invalid entities", cause)

	if !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("error = %v, want ErrInvalidEvent", err)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("error = %v, want wrapped cause", err)
	}
	if !strings.Contains(err.Error(), "article.created invalid entities") {
		t.Fatalf("error = %q, want detail in message", err.Error())
	}
}

func TestEventValidateRejectsInvalidArticleTags(t *testing.T) {
	t.Parallel()

	event := &Event{
		ID:         "evt-1",
		Kind:       EventKindArticleCreated,
		OccurredAt: time.Unix(1, 0).UTC(),
		Source: EventSource{
			Platform: PlatformTelegram,
			ID:       "tg-main",
		},
		Conversation: Conversation{
			ID:   "chat-1",
			Type: ConversationTypeGroup,
		},
		Article: &Article{
			ID:   "m-1",
			Text: "hello",
			Tags: map[string]string{
				"": "bad",
			},
		},
	}

	err := event.Validate()
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("error = %v, want ErrInvalidEvent", err)
	}
	if !strings.Contains(err.Error(), "invalid tags") {
		t.Fatalf("error = %q, want invalid tags detail", err.Error())
	}
}

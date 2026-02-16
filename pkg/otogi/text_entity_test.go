package otogi

import (
	"errors"
	"testing"
	"time"
)

func TestValidateTextEntities(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		text     string
		entities []TextEntity
		wantErr  bool
	}{
		{
			name: "empty entities are valid",
			text: "hello",
		},
		{
			name: "valid simple style entity",
			text: "hello",
			entities: []TextEntity{
				{Type: TextEntityTypeBold, Offset: 0, Length: 5},
			},
		},
		{
			name: "valid text url entity",
			text: "click me",
			entities: []TextEntity{
				{Type: TextEntityTypeTextURL, Offset: 0, Length: 8, URL: "https://example.com"},
			},
		},
		{
			name: "valid pre entity with language",
			text: "fmt.Println()",
			entities: []TextEntity{
				{Type: TextEntityTypePre, Offset: 0, Length: 12, Language: "go"},
			},
		},
		{
			name: "valid mention name entity",
			text: "Alice",
			entities: []TextEntity{
				{Type: TextEntityTypeMentionName, Offset: 0, Length: 5, MentionUserID: "123"},
			},
		},
		{
			name: "valid custom emoji entity",
			text: "😀",
			entities: []TextEntity{
				{Type: TextEntityTypeCustomEmoji, Offset: 0, Length: 1, CustomEmojiID: "999"},
			},
		},
		{
			name: "missing type fails",
			text: "hello",
			entities: []TextEntity{
				{Offset: 0, Length: 5},
			},
			wantErr: true,
		},
		{
			name: "negative offset fails",
			text: "hello",
			entities: []TextEntity{
				{Type: TextEntityTypeBold, Offset: -1, Length: 1},
			},
			wantErr: true,
		},
		{
			name: "non-positive length fails",
			text: "hello",
			entities: []TextEntity{
				{Type: TextEntityTypeBold, Offset: 0, Length: 0},
			},
			wantErr: true,
		},
		{
			name: "range overflow fails",
			text: "hello",
			entities: []TextEntity{
				{Type: TextEntityTypeBold, Offset: 3, Length: 3},
			},
			wantErr: true,
		},
		{
			name: "text_url without url fails",
			text: "click me",
			entities: []TextEntity{
				{Type: TextEntityTypeTextURL, Offset: 0, Length: 8},
			},
			wantErr: true,
		},
		{
			name: "mention_name without id fails",
			text: "alice",
			entities: []TextEntity{
				{Type: TextEntityTypeMentionName, Offset: 0, Length: 5},
			},
			wantErr: true,
		},
		{
			name: "custom_emoji without id fails",
			text: "😀",
			entities: []TextEntity{
				{Type: TextEntityTypeCustomEmoji, Offset: 0, Length: 1},
			},
			wantErr: true,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			err := ValidateTextEntities(testCase.text, testCase.entities)
			if testCase.wantErr && err == nil {
				t.Fatal("expected validation error")
			}
			if !testCase.wantErr && err != nil {
				t.Fatalf("unexpected validation error: %v", err)
			}
		})
	}
}

func TestEventValidateMessageEntityContract(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		event   *Event
		wantErr bool
	}{
		{
			name: "valid created event with entities",
			event: &Event{
				ID:         "evt-1",
				Kind:       EventKindMessageCreated,
				OccurredAt: time.Unix(1, 0).UTC(),
				Platform:   PlatformTelegram,
				Conversation: Conversation{
					ID:   "chat-1",
					Type: ConversationTypeGroup,
				},
				Message: &Message{
					ID:   "msg-1",
					Text: "hello",
					Entities: []TextEntity{
						{Type: TextEntityTypeBold, Offset: 0, Length: 5},
					},
				},
			},
		},
		{
			name: "created event missing message id fails",
			event: &Event{
				ID:         "evt-2",
				Kind:       EventKindMessageCreated,
				OccurredAt: time.Unix(1, 0).UTC(),
				Platform:   PlatformTelegram,
				Conversation: Conversation{
					ID:   "chat-1",
					Type: ConversationTypeGroup,
				},
				Message: &Message{
					Text: "hello",
				},
			},
			wantErr: true,
		},
		{
			name: "created event invalid entity range fails",
			event: &Event{
				ID:         "evt-3",
				Kind:       EventKindMessageCreated,
				OccurredAt: time.Unix(1, 0).UTC(),
				Platform:   PlatformTelegram,
				Conversation: Conversation{
					ID:   "chat-1",
					Type: ConversationTypeGroup,
				},
				Message: &Message{
					ID:   "msg-1",
					Text: "hello",
					Entities: []TextEntity{
						{Type: TextEntityTypeBold, Offset: 0, Length: 6},
					},
				},
			},
			wantErr: true,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			err := testCase.event.Validate()
			if testCase.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				if !errors.Is(err, ErrInvalidEvent) {
					t.Fatalf("error = %v, want ErrInvalidEvent", err)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

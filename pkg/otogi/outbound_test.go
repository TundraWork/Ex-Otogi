package otogi

import (
	"errors"
	"testing"
	"time"
)

func TestOutboundRequestValidation(t *testing.T) {
	t.Parallel()

	validTarget := OutboundTarget{
		Platform: PlatformTelegram,
		Conversation: Conversation{
			ID:   "chat-1",
			Type: ConversationTypeGroup,
		},
	}

	tests := []struct {
		name    string
		check   func() error
		wantErr bool
	}{
		{
			name: "send message valid",
			check: func() error {
				return SendMessageRequest{
					Target: validTarget,
					Text:   "hello",
				}.Validate()
			},
		},
		{
			name: "send message missing text",
			check: func() error {
				return SendMessageRequest{
					Target: validTarget,
				}.Validate()
			},
			wantErr: true,
		},
		{
			name: "edit message valid",
			check: func() error {
				return EditMessageRequest{
					Target:    validTarget,
					MessageID: "100",
					Text:      "updated",
				}.Validate()
			},
		},
		{
			name: "edit message missing id",
			check: func() error {
				return EditMessageRequest{
					Target: validTarget,
					Text:   "updated",
				}.Validate()
			},
			wantErr: true,
		},
		{
			name: "delete message valid",
			check: func() error {
				return DeleteMessageRequest{
					Target:    validTarget,
					MessageID: "100",
				}.Validate()
			},
		},
		{
			name: "delete message missing id",
			check: func() error {
				return DeleteMessageRequest{
					Target: validTarget,
				}.Validate()
			},
			wantErr: true,
		},
		{
			name: "set reaction add valid",
			check: func() error {
				return SetReactionRequest{
					Target:    validTarget,
					MessageID: "100",
					Emoji:     "👍",
					Action:    ReactionActionAdd,
				}.Validate()
			},
		},
		{
			name: "set reaction remove valid without emoji",
			check: func() error {
				return SetReactionRequest{
					Target:    validTarget,
					MessageID: "100",
					Action:    ReactionActionRemove,
				}.Validate()
			},
		},
		{
			name: "set reaction add missing emoji",
			check: func() error {
				return SetReactionRequest{
					Target:    validTarget,
					MessageID: "100",
					Action:    ReactionActionAdd,
				}.Validate()
			},
			wantErr: true,
		},
		{
			name: "invalid target platform",
			check: func() error {
				return SendMessageRequest{
					Target: OutboundTarget{
						Conversation: Conversation{ID: "chat-1", Type: ConversationTypeGroup},
					},
					Text: "hello",
				}.Validate()
			},
			wantErr: true,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			err := testCase.check()
			if testCase.wantErr && err == nil {
				t.Fatal("expected validation error")
			}
			if !testCase.wantErr && err != nil {
				t.Fatalf("unexpected validation error: %v", err)
			}
			if err != nil && !errors.Is(err, ErrInvalidOutboundRequest) {
				t.Fatalf("error = %v, want ErrInvalidOutboundRequest", err)
			}
		})
	}
}

func TestOutboundTargetFromEvent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		event   *Event
		wantErr bool
	}{
		{
			name: "valid event",
			event: &Event{
				ID:         "evt-1",
				Kind:       EventKindMessageCreated,
				OccurredAt: time.Unix(1, 0).UTC(),
				Platform:   PlatformTelegram,
				Conversation: Conversation{
					ID:   "chat-1",
					Type: ConversationTypePrivate,
				},
				Message: &Message{ID: "msg-1", Text: "hello"},
			},
		},
		{
			name:    "nil event",
			event:   nil,
			wantErr: true,
		},
		{
			name: "missing conversation",
			event: &Event{
				ID:         "evt-2",
				Kind:       EventKindMessageCreated,
				OccurredAt: time.Unix(1, 0).UTC(),
				Platform:   PlatformTelegram,
				Message:    &Message{ID: "msg-1", Text: "hello"},
			},
			wantErr: true,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			target, err := OutboundTargetFromEvent(testCase.event)
			if testCase.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				if !errors.Is(err, ErrInvalidOutboundRequest) {
					t.Fatalf("error = %v, want ErrInvalidOutboundRequest", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if target.Platform != PlatformTelegram {
				t.Fatalf("target platform = %s, want %s", target.Platform, PlatformTelegram)
			}
			if target.Conversation.ID != "chat-1" {
				t.Fatalf("target conversation id = %s, want chat-1", target.Conversation.ID)
			}
		})
	}
}

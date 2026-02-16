package otogi

import (
	"testing"
	"time"
)

func TestMessageLookupValidate(t *testing.T) {
	tests := []struct {
		name    string
		lookup  MessageLookup
		wantErr bool
	}{
		{
			name: "valid lookup",
			lookup: MessageLookup{
				Platform:       PlatformTelegram,
				ConversationID: "chat-1",
				MessageID:      "msg-1",
			},
		},
		{
			name: "missing platform",
			lookup: MessageLookup{
				ConversationID: "chat-1",
				MessageID:      "msg-1",
			},
			wantErr: true,
		},
		{
			name: "missing conversation id",
			lookup: MessageLookup{
				Platform:  PlatformTelegram,
				MessageID: "msg-1",
			},
			wantErr: true,
		},
		{
			name: "missing message id",
			lookup: MessageLookup{
				Platform:       PlatformTelegram,
				ConversationID: "chat-1",
			},
			wantErr: true,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			err := testCase.lookup.Validate()
			if testCase.wantErr && err == nil {
				t.Fatal("expected validation error")
			}
			if !testCase.wantErr && err != nil {
				t.Fatalf("unexpected validation error: %v", err)
			}
		})
	}
}

func TestMessageLookupFromEvent(t *testing.T) {
	tests := []struct {
		name       string
		event      *Event
		want       MessageLookup
		wantErr    bool
		assertFunc func(*testing.T, MessageLookup)
	}{
		{
			name: "valid message event",
			event: &Event{
				ID:         "evt-1",
				Kind:       EventKindMessageCreated,
				OccurredAt: time.Unix(1, 0).UTC(),
				Platform:   PlatformTelegram,
				Conversation: Conversation{
					ID: "chat-1",
				},
				Message: &Message{ID: "msg-1"},
			},
			want: MessageLookup{
				Platform:       PlatformTelegram,
				ConversationID: "chat-1",
				MessageID:      "msg-1",
			},
		},
		{
			name:    "nil event",
			event:   nil,
			wantErr: true,
		},
		{
			name: "missing message payload",
			event: &Event{
				Kind:       EventKindMessageCreated,
				OccurredAt: time.Unix(1, 0).UTC(),
				Platform:   PlatformTelegram,
				Conversation: Conversation{
					ID: "chat-1",
				},
			},
			wantErr: true,
		},
		{
			name: "missing message id",
			event: &Event{
				Kind:       EventKindMessageCreated,
				OccurredAt: time.Unix(1, 0).UTC(),
				Platform:   PlatformTelegram,
				Conversation: Conversation{
					ID: "chat-1",
				},
				Message: &Message{},
			},
			wantErr: true,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got, err := MessageLookupFromEvent(testCase.event)
			if testCase.wantErr && err == nil {
				t.Fatal("expected error")
			}
			if !testCase.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if testCase.wantErr {
				return
			}

			if got != testCase.want {
				t.Fatalf("lookup = %#v, want %#v", got, testCase.want)
			}
			if testCase.assertFunc != nil {
				testCase.assertFunc(t, got)
			}
		})
	}
}

func TestReplyLookupFromEvent(t *testing.T) {
	tests := []struct {
		name    string
		event   *Event
		want    MessageLookup
		wantErr bool
	}{
		{
			name: "valid reply event",
			event: &Event{
				ID:         "evt-1",
				Kind:       EventKindMessageCreated,
				OccurredAt: time.Unix(1, 0).UTC(),
				Platform:   PlatformTelegram,
				Conversation: Conversation{
					ID: "chat-1",
				},
				Message: &Message{ID: "msg-2", ReplyToID: "msg-1"},
			},
			want: MessageLookup{
				Platform:       PlatformTelegram,
				ConversationID: "chat-1",
				MessageID:      "msg-1",
			},
		},
		{
			name:    "nil event",
			event:   nil,
			wantErr: true,
		},
		{
			name: "missing message payload",
			event: &Event{
				Kind:       EventKindMessageCreated,
				OccurredAt: time.Unix(1, 0).UTC(),
				Platform:   PlatformTelegram,
				Conversation: Conversation{
					ID: "chat-1",
				},
			},
			wantErr: true,
		},
		{
			name: "missing reply id",
			event: &Event{
				Kind:       EventKindMessageCreated,
				OccurredAt: time.Unix(1, 0).UTC(),
				Platform:   PlatformTelegram,
				Conversation: Conversation{
					ID: "chat-1",
				},
				Message: &Message{ID: "msg-2"},
			},
			wantErr: true,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got, err := ReplyLookupFromEvent(testCase.event)
			if testCase.wantErr && err == nil {
				t.Fatal("expected error")
			}
			if !testCase.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if testCase.wantErr {
				return
			}
			if got != testCase.want {
				t.Fatalf("lookup = %#v, want %#v", got, testCase.want)
			}
		})
	}
}

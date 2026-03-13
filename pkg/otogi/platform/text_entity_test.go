package platform

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
			name: "valid heading entity",
			text: "## heading",
			entities: []TextEntity{
				{
					Type:   TextEntityTypeHeading,
					Offset: 0,
					Length: 10,
					Heading: &TextEntityHeadingMeta{
						Level: 2,
					},
				},
			},
		},
		{
			name: "valid list item entity",
			text: "- item",
			entities: []TextEntity{
				{
					Type:   TextEntityTypeListItem,
					Offset: 0,
					Length: 6,
					List: &TextEntityListMeta{
						Depth:      1,
						Ordered:    false,
						ItemNumber: 0,
					},
				},
			},
		},
		{
			name: "valid task item entity",
			text: "- [x] done",
			entities: []TextEntity{
				{
					Type:   TextEntityTypeTaskItem,
					Offset: 0,
					Length: 10,
					List: &TextEntityListMeta{
						Depth:      1,
						Ordered:    false,
						ItemNumber: 0,
					},
					Task: &TextEntityTaskMeta{
						Checked: true,
					},
				},
			},
		},
		{
			name: "valid table cell entity",
			text: "| a |",
			entities: []TextEntity{
				{
					Type:   TextEntityTypeTableCell,
					Offset: 2,
					Length: 1,
					Table: &TextEntityTableMeta{
						GroupID:   "table:1",
						Row:       0,
						Column:    0,
						Header:    true,
						Alignment: "left",
					},
				},
			},
		},
		{
			name: "valid image entity",
			text: "![alt](https://example.com)",
			entities: []TextEntity{
				{
					Type:   TextEntityTypeImage,
					Offset: 0,
					Length: 27,
					Image: &TextEntityImageMeta{
						URL:   "https://example.com",
						Alt:   "alt",
						Title: "",
					},
				},
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
		{
			name: "heading without metadata fails",
			text: "## heading",
			entities: []TextEntity{
				{Type: TextEntityTypeHeading, Offset: 0, Length: 10},
			},
			wantErr: true,
		},
		{
			name: "list depth must be positive",
			text: "- item",
			entities: []TextEntity{
				{
					Type:   TextEntityTypeList,
					Offset: 0,
					Length: 6,
					List: &TextEntityListMeta{
						Depth: 0,
					},
				},
			},
			wantErr: true,
		},
		{
			name: "ordered list item requires item number",
			text: "1. item",
			entities: []TextEntity{
				{
					Type:   TextEntityTypeListItem,
					Offset: 0,
					Length: 7,
					List: &TextEntityListMeta{
						Depth:      1,
						Ordered:    true,
						ItemNumber: 0,
					},
				},
			},
			wantErr: true,
		},
		{
			name: "task item requires task metadata",
			text: "- [x] done",
			entities: []TextEntity{
				{
					Type:   TextEntityTypeTaskItem,
					Offset: 0,
					Length: 10,
					List: &TextEntityListMeta{
						Depth: 1,
					},
				},
			},
			wantErr: true,
		},
		{
			name: "table cell requires group id",
			text: "| a |",
			entities: []TextEntity{
				{
					Type:   TextEntityTypeTableCell,
					Offset: 2,
					Length: 1,
					Table: &TextEntityTableMeta{
						Row:    0,
						Column: 0,
					},
				},
			},
			wantErr: true,
		},
		{
			name: "image requires url",
			text: "![alt]()",
			entities: []TextEntity{
				{
					Type:   TextEntityTypeImage,
					Offset: 0,
					Length: 8,
					Image: &TextEntityImageMeta{
						Alt: "alt",
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

func TestEventValidateArticleEntityContract(t *testing.T) {
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
				Kind:       EventKindArticleCreated,
				OccurredAt: time.Unix(1, 0).UTC(),
				Source: EventSource{
					Platform: PlatformTelegram,
				},
				Conversation: Conversation{
					ID:   "chat-1",
					Type: ConversationTypeGroup,
				},
				Article: &Article{
					ID:   "msg-1",
					Text: "hello",
					Entities: []TextEntity{
						{Type: TextEntityTypeBold, Offset: 0, Length: 5},
					},
				},
			},
		},
		{
			name: "created event missing article id fails",
			event: &Event{
				ID:         "evt-2",
				Kind:       EventKindArticleCreated,
				OccurredAt: time.Unix(1, 0).UTC(),
				Source: EventSource{
					Platform: PlatformTelegram,
				},
				Conversation: Conversation{
					ID:   "chat-1",
					Type: ConversationTypeGroup,
				},
				Article: &Article{
					Text: "hello",
				},
			},
			wantErr: true,
		},
		{
			name: "created event invalid entity range fails",
			event: &Event{
				ID:         "evt-3",
				Kind:       EventKindArticleCreated,
				OccurredAt: time.Unix(1, 0).UTC(),
				Source: EventSource{
					Platform: PlatformTelegram,
				},
				Conversation: Conversation{
					ID:   "chat-1",
					Type: ConversationTypeGroup,
				},
				Article: &Article{
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

func TestEventValidateMutationSnapshotEntityContract(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		event   *Event
		wantErr bool
	}{
		{
			name: "valid edited event with snapshot entities",
			event: &Event{
				ID:         "evt-10",
				Kind:       EventKindArticleEdited,
				OccurredAt: time.Unix(1, 0).UTC(),
				Source: EventSource{
					Platform: PlatformTelegram,
				},
				Conversation: Conversation{
					ID:   "chat-1",
					Type: ConversationTypeGroup,
				},
				Mutation: &ArticleMutation{
					Type:            MutationTypeEdit,
					TargetArticleID: "msg-1",
					Before: &ArticleSnapshot{
						Text: "hello",
						Entities: []TextEntity{
							{Type: TextEntityTypeBold, Offset: 0, Length: 5},
						},
					},
					After: &ArticleSnapshot{
						Text: "hello edited",
						Entities: []TextEntity{
							{Type: TextEntityTypeItalic, Offset: 0, Length: 11},
						},
					},
				},
			},
		},
		{
			name: "invalid before snapshot entity range fails",
			event: &Event{
				ID:         "evt-11",
				Kind:       EventKindArticleEdited,
				OccurredAt: time.Unix(1, 0).UTC(),
				Source: EventSource{
					Platform: PlatformTelegram,
				},
				Conversation: Conversation{
					ID:   "chat-1",
					Type: ConversationTypeGroup,
				},
				Mutation: &ArticleMutation{
					Type:            MutationTypeEdit,
					TargetArticleID: "msg-1",
					Before: &ArticleSnapshot{
						Text: "hello",
						Entities: []TextEntity{
							{Type: TextEntityTypeBold, Offset: 0, Length: 6},
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "invalid after snapshot text_url entity fails",
			event: &Event{
				ID:         "evt-12",
				Kind:       EventKindArticleEdited,
				OccurredAt: time.Unix(1, 0).UTC(),
				Source: EventSource{
					Platform: PlatformTelegram,
				},
				Conversation: Conversation{
					ID:   "chat-1",
					Type: ConversationTypeGroup,
				},
				Mutation: &ArticleMutation{
					Type:            MutationTypeEdit,
					TargetArticleID: "msg-1",
					After: &ArticleSnapshot{
						Text: "updated",
						Entities: []TextEntity{
							{Type: TextEntityTypeTextURL, Offset: 0, Length: 7},
						},
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

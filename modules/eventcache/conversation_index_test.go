package eventcache

import (
	"testing"
	"time"
)

func TestInsertConversationStreamEntry(t *testing.T) {
	t.Parallel()

	base := time.Unix(10, 0).UTC()
	tests := []struct {
		name      string
		existing  []conversationStreamEntry
		insert    conversationStreamEntry
		wantOrder []string
	}{
		{
			name: "insert earliest entry at front",
			existing: []conversationStreamEntry{
				newConversationStreamEntry("msg-2", base.Add(20*time.Second), 2),
				newConversationStreamEntry("msg-3", base.Add(30*time.Second), 3),
			},
			insert:    newConversationStreamEntry("msg-1", base.Add(10*time.Second), 1),
			wantOrder: []string{"msg-1", "msg-2", "msg-3"},
		},
		{
			name: "insert between existing timestamps",
			existing: []conversationStreamEntry{
				newConversationStreamEntry("msg-1", base.Add(10*time.Second), 1),
				newConversationStreamEntry("msg-3", base.Add(30*time.Second), 3),
			},
			insert:    newConversationStreamEntry("msg-2", base.Add(20*time.Second), 2),
			wantOrder: []string{"msg-1", "msg-2", "msg-3"},
		},
		{
			name: "insert tie after lower sequence",
			existing: []conversationStreamEntry{
				newConversationStreamEntry("msg-1", base.Add(10*time.Second), 1),
				newConversationStreamEntry("msg-3", base.Add(10*time.Second), 3),
			},
			insert:    newConversationStreamEntry("msg-2", base.Add(10*time.Second), 2),
			wantOrder: []string{"msg-1", "msg-2", "msg-3"},
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got := insertConversationStreamEntry(append([]conversationStreamEntry(nil), testCase.existing...), testCase.insert)
			if len(got) != len(testCase.wantOrder) {
				t.Fatalf("entry count = %d, want %d", len(got), len(testCase.wantOrder))
			}
			for index, wantKey := range testCase.wantOrder {
				if got[index].key.articleID != wantKey {
					t.Fatalf("entries[%d].key.articleID = %q, want %q", index, got[index].key.articleID, wantKey)
				}
			}
		})
	}
}

func TestLocateConversationStreamEntry(t *testing.T) {
	t.Parallel()

	base := time.Unix(10, 0).UTC()
	entries := []conversationStreamEntry{
		newConversationStreamEntry("msg-1", base.Add(10*time.Second), 1),
		newConversationStreamEntry("msg-2", base.Add(20*time.Second), 2),
		newConversationStreamEntry("msg-3", base.Add(20*time.Second), 3),
	}

	tests := []struct {
		name   string
		target conversationStreamEntry
		want   int
	}{
		{
			name:   "locates middle entry",
			target: newConversationStreamEntry("msg-2", base.Add(20*time.Second), 2),
			want:   1,
		},
		{
			name:   "locates tied timestamp by sequence",
			target: newConversationStreamEntry("msg-3", base.Add(20*time.Second), 3),
			want:   2,
		},
		{
			name:   "missing createdAt does not match",
			target: newConversationStreamEntry("msg-2", base.Add(21*time.Second), 2),
			want:   -1,
		},
		{
			name:   "missing key returns not found",
			target: newConversationStreamEntry("msg-9", base.Add(20*time.Second), 2),
			want:   -1,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got := locateConversationStreamEntry(entries, testCase.target)
			if got != testCase.want {
				t.Fatalf("locateConversationStreamEntry() = %d, want %d", got, testCase.want)
			}
		})
	}
}

func newConversationStreamEntry(
	articleID string,
	createdAt time.Time,
	sequence uint64,
) conversationStreamEntry {
	return conversationStreamEntry{
		key: cacheKey{
			tenantID:       "tenant-1",
			platform:       "telegram",
			conversationID: "chat-1",
			articleID:      articleID,
		},
		createdAt: createdAt,
		sequence:  sequence,
	}
}

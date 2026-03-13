package llmchat

import (
	"testing"
	"time"

	"ex-otogi/pkg/otogi"
)

func TestSelectLeadingContextEntries(t *testing.T) {
	t.Parallel()

	entries := []otogi.ConversationContextEntry{
		newLeadingContextEntry("ctx-1", "first context"),
		newLeadingContextEntry("ctx-2", "second context"),
		newLeadingContextEntry("ctx-3", "third context"),
	}
	fullBudget := runeCount(
		serializeLeadingContextMessage(entries, "messages_before_current_message", 200, nil, false).content,
	)
	suffixBudget := runeCount(
		serializeLeadingContextMessage(entries[1:], "messages_before_current_message", 200, nil, false).content,
	)

	tests := []struct {
		name           string
		budget         int
		wantArticleIDs []string
		wantOmitted    int
	}{
		{
			name:           "full budget keeps all entries",
			budget:         fullBudget,
			wantArticleIDs: []string{"ctx-1", "ctx-2", "ctx-3"},
			wantOmitted:    0,
		},
		{
			name:           "tight budget keeps newest suffix",
			budget:         suffixBudget,
			wantArticleIDs: []string{"ctx-2", "ctx-3"},
			wantOmitted:    1,
		},
		{
			name:           "zero budget omits all entries",
			budget:         0,
			wantArticleIDs: nil,
			wantOmitted:    len(entries),
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			selected, omitted := selectLeadingContextEntries(
				entries,
				testCase.budget,
				"messages_before_current_message",
				200,
				nil,
				false,
			)
			if omitted != testCase.wantOmitted {
				t.Fatalf("omitted = %d, want %d", omitted, testCase.wantOmitted)
			}
			if len(selected) != len(testCase.wantArticleIDs) {
				t.Fatalf("selected len = %d, want %d", len(selected), len(testCase.wantArticleIDs))
			}
			for index, wantArticleID := range testCase.wantArticleIDs {
				if selected[index].Article.ID != wantArticleID {
					t.Fatalf("selected[%d].Article.ID = %q, want %q", index, selected[index].Article.ID, wantArticleID)
				}
			}
		})
	}
}

func TestRuneCountCountsUTF8RunesWithoutAllocationSensitiveSliceLogic(t *testing.T) {
	t.Parallel()

	if got := runeCount("你🙂a"); got != 3 {
		t.Fatalf("runeCount() = %d, want 3", got)
	}
}

func newLeadingContextEntry(articleID string, text string) otogi.ConversationContextEntry {
	return otogi.ConversationContextEntry{
		Conversation: otogi.Conversation{ID: "chat-1", Type: otogi.ConversationTypeGroup},
		Actor:        otogi.Actor{ID: "user-1", Username: "alice"},
		Article:      otogi.Article{ID: articleID, Text: text},
		CreatedAt:    time.Unix(100, 0).UTC(),
	}
}

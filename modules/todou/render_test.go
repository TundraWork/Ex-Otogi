package todou

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

func TestDigestRendering(t *testing.T) {
	t.Parallel()
	r := renderer{linkBase: "https://todou.test", maxBody: 10}
	hidden := "2026-10-16T00:00:00Z"
	issues := map[string]issue{"web#1": {Title: "T1"}, "web#2": {Title: "A title much longer than forty characters in all", Body: "[long](/projects/3/issues/9) opened body"}}
	entries := []entry{
		{Type: "comment", Project: "web", IssueNumber: 1, IssueRef: "web#1", Author: &user{DisplayName: "Alice"}, Body: "**bold** `code` more", AgentContext: &agentContext{Agent: "claude-code", Model: "claude-opus-5-5"}},
		{Type: "event", EventType: "opened", Project: "web", IssueNumber: 2, Actor: &user{Login: "c", Kind: "machine"}},
		{Type: "comment", Project: "web", IssueNumber: 1, IssueRef: "web#1", Author: &user{Login: "bob"}, HiddenAt: &hidden},
		{Type: "event", EventType: "status_changed", Project: "web", IssueNumber: 1, IssueRef: "web#1", Actor: &user{Login: "bob"},
			Payload: map[string]any{"from": map[string]any{"name": "Todo"}, "to": map[string]any{"name": "Doing"}}},
		{Type: "event", EventType: "brand_new_kind", Project: "web", IssueNumber: 2, Actor: &user{Login: "c", Kind: "machine"}},
	}
	msgs := r.digest(entries, issues)
	if len(msgs) != 1 {
		t.Fatalf("messages = %d", len(msgs))
	}
	part := msgs[0].Parts[0]
	want := "🔔 5 todou updates\n\n" +
		"web#1 T1\n💬 🤖 Alice (claude-opus-5-5) commented\nbold code…\n💬 bob commented (hidden)\n🔀 bob moved it Todo → Doing\n\n" +
		"web#2 A title much longer than forty character…\n🆕 🤖 c opened it\nlong opene…\n• 🤖 c brand new kind"
	if part.Text != want {
		t.Fatalf("text:\n%s\nwant:\n%s", part.Text, want)
	}
	var marks []string
	for _, a := range part.Annotations {
		marks = append(marks, fmt.Sprintf("%s:%s", a.Kind, part.Text[a.Offset:a.Offset+a.Length]))
	}
	for _, m := range []string{"bold:🔔 5 todou updates", "link:web#1 T1", "bold:web#1", "link:web#2 A title much longer than forty character…", "bold:bold", "code:code", "block_quote:bold code…", "code:Todo", "code:Doing", "block_quote:long opene…"} {
		if !slices.Contains(marks, m) {
			t.Errorf("annotations lack %s: %v", m, marks)
		}
	}
	if link := part.Annotations[slices.IndexFunc(part.Annotations, func(a otogi.Annotation) bool { return a.Kind == otogi.AnnotationLink })]; link.Target != "https://todou.test/projects/web/issues/1" {
		t.Errorf("link = %+v", link)
	}
	if !slices.ContainsFunc(part.Annotations, func(a otogi.Annotation) bool { return a.Target == "https://todou.test/projects/3/issues/9" }) {
		t.Errorf("a relative body link was not resolved: %+v", part.Annotations)
	}
	if err := msgs[0].Validate(); err != nil {
		t.Fatal(err)
	}

	single := r.digest(entries[:1], issues)
	if text := single[0].PlainText(); !strings.HasPrefix(text, "web#1 T1\n💬") {
		t.Errorf("a single entry has a header: %q", text)
	}
}

func TestLongDigestsSplitAndRepeatTheIssueLine(t *testing.T) {
	t.Parallel()
	r := renderer{linkBase: "https://todou.test", maxBody: 300}
	many := make([]entry, 60)
	for i := range many {
		many[i] = entry{Type: "comment", Project: "web", IssueNumber: 1, IssueRef: "web#1", Author: &user{Login: "a"}, Body: strings.Repeat("汉字", 150)}
	}
	split := r.digest(many, nil)
	if len(split) < 2 {
		t.Fatalf("split into %d messages", len(split))
	}
	for _, m := range split {
		if n := utf16Len(m.PlainText()); n > messageLimit {
			t.Fatalf("message of %d UTF-16 units exceeds the limit", n)
		}
		if err := m.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	if !strings.HasPrefix(split[1].PlainText(), "web#1\n💬 a commented") {
		t.Fatalf("continued message starts %q", split[1].PlainText()[:40])
	}
}

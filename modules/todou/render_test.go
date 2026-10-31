package todou

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

func TestDigestRendering(t *testing.T) {
	t.Parallel()
	r := renderer{links: map[string]string{"": "https://todou.test"}, maxBody: 10}
	hidden := "2026-10-16T00:00:00Z"
	issues := facts{issues: map[string]issue{titleKey("", "web", 1): {Title: "T1"}, titleKey("", "web", 2): {Title: "A title much longer than forty characters in all", Body: "[long](/projects/3/issues/9) opened body"}}}
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
	want := "web#1 T1\n💬 👾 Alice (claude-opus-5-5) commented\nbold code…\n💬 bob commented (hidden)\n🔀 bob moved it Todo → Doing\n\n" +
		"web#2 A title much longer than forty character…\n🆕 👾 c opened it\nlong opene…\n• 👾 c brand new kind"
	if part.Text != want {
		t.Fatalf("text:\n%s\nwant:\n%s", part.Text, want)
	}
	var marks []string
	for _, a := range part.Annotations {
		marks = append(marks, fmt.Sprintf("%s:%s", a.Kind, part.Text[a.Offset:a.Offset+a.Length]))
	}
	for _, m := range []string{"link:web#1", "bold:web#1 T1", "bold:Alice", "bold:bob", "link:web#2", "bold:web#2 A title much longer than forty character…", "bold:bold", "code:code", "expandable_block_quote:bold code…", "code:Todo", "code:Doing", "expandable_block_quote:long opene…"} {
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

}

func TestLongDigestsSplitAndRepeatTheIssueLine(t *testing.T) {
	t.Parallel()
	r := renderer{links: map[string]string{"": "https://todou.test"}, maxBody: 300}
	many := make([]entry, 60)
	for i := range many {
		many[i] = entry{Type: "comment", Project: "web", IssueNumber: 1, IssueRef: "web#1", Author: &user{Login: "a"}, Body: strings.Repeat("汉字", 150)}
	}
	split := r.digest(many, facts{})
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

func TestQuestionsAndAnswers(t *testing.T) {
	t.Parallel()
	r := renderer{links: map[string]string{"": "https://todou.test"}, maxBody: 1000}
	var asking entry
	if err := json.Unmarshal([]byte(`{"type":"comment","id":939,"project":"web","issue_number":1,"issue_ref":"web#1","body":"Two choices.",
		"author":{"login":"agent","kind":"machine"},"agent_context":{"agent":"claude-code","model":"claude-opus-5"},
		"component":{"type":"questions","questions":[
			{"key":"q1","header":"Split","question":"How to split the card?","options":[{"label":"Three cards","description":"…"},{"label":"One card"}]},
			{"key":"q2","header":"Ship","question":"How to ship?","options":[{"label":"Push"},{"label":"Local"}]}]}}`), &asking); err != nil {
		t.Fatal(err)
	}
	var answered entry
	if err := json.Unmarshal([]byte(`{"type":"event","event_type":"question_answered","project":"web","issue_number":1,"issue_ref":"web#1",
		"actor":{"login":"john","display_name":"John","kind":"human"},
		"payload":{"via":"answer","comment_id":939,"answers":[
			{"key":"q1","other":"start with the first","declined":false,"selected":[{"index":0,"label":"Three cards"}]},
			{"key":"q2","other":null,"declined":true,"selected":[]},
			{"key":"q9","other":"unknown question","declined":false,"selected":[]}]}}`), &answered); err != nil {
		t.Fatal(err)
	}
	known := facts{issues: map[string]issue{titleKey("", "web", 1): {Title: "T"}}, questions: map[string][]question{questionKey("", 939): asking.Component.Questions}}
	text := r.digest([]entry{asking, answered}, known)[0].PlainText()
	want := "web#1 T\n" +
		"❓ 👾 agent (claude-opus-5) asked\nTwo choices.\nSplit: How to split the card?\n• Three cards\n• One card\n\nShip: How to ship?\n• Push\n• Local\n" +
		"🙋 John answered\nSplit: How to split the card?\n→ Three cards\n→ start with the first\n\nShip: How to ship?\n→ (declined)\n\nq9\n→ unknown question"
	if text != want {
		t.Fatalf("text:\n%s\nwant:\n%s", text, want)
	}
}

func TestOversizedEntriesContinueWithAllFormatting(t *testing.T) {
	t.Parallel()
	r := renderer{links: map[string]string{"": "https://todou.test"}, maxBody: 3000}
	codeHeavy := strings.Repeat("`a.go` ", 300) // 300 code spans on one line
	long := strings.Repeat("**长**\n", 1400)     // 2800 units over many lines
	entries := []entry{
		{Type: "comment", Project: "web", IssueNumber: 1, IssueRef: "web#1", Author: &user{Login: "a"}, Body: codeHeavy},
		{Type: "comment", Project: "web", IssueNumber: 2, IssueRef: "web#2", Author: &user{Login: "b"}, Body: long,
			Component: &component{Questions: []question{{Key: "q1", Header: "H", Question: long}}}},
	}
	msgs := r.digest(entries, facts{})
	code, boldLong := 0, 0
	for i, m := range msgs {
		part := m.Parts[0]
		if n := len(part.Annotations); n > entityLimit {
			t.Fatalf("message %d has %d annotations", i, n)
		}
		if n := utf16Len(part.Text); n > messageLimit {
			t.Fatalf("message %d has %d UTF-16 units", i, n)
		}
		if err := m.Validate(); err != nil {
			t.Fatal(err)
		}
		if i > 0 && !strings.HasPrefix(part.Text, "web#") {
			t.Errorf("message %d does not name its issue: %q", i, part.Text[:20])
		}
		for _, a := range part.Annotations {
			switch s := part.Text[a.Offset : a.Offset+a.Length]; {
			case a.Kind == otogi.AnnotationCode && s == "a.go":
				code++
			case a.Kind == otogi.AnnotationBold && s == "长":
				boldLong++
			}
		}
	}
	if code != 300 || boldLong != 2800 {
		t.Fatalf("formatting kept: %d of 300 code spans, %d of 2800 bold runs, over %d messages", code, boldLong, len(msgs))
	}
}

func TestNewEventTypes(t *testing.T) {
	t.Parallel()
	r := renderer{links: map[string]string{"neptune": "https://todou.test"}, maxBody: 100}
	ev := func(kind string, payload map[string]any) entry {
		return entry{Type: "event", EventType: kind, Project: "web", IssueNumber: 5, IssueRef: "WEB-5", Actor: &user{Login: "bob"}, Payload: payload, agent: "neptune"}
	}
	var payload map[string]any
	_ = json.Unmarshal([]byte(`{"role":"blocked","edge_id":1,"other_number":77,"other_project_id":17,"other_project":"web"}`), &payload)
	var blocks map[string]any
	_ = json.Unmarshal([]byte(`{"role":"blocker","edge_id":2,"other_number":9,"other_project_id":23,"other_project":"api"}`), &blocks)
	var cleared map[string]any
	_ = json.Unmarshal([]byte(`{"edge_id":1,"blocker_number":77,"blocker_project_id":17,"blocker_project":"web"}`), &cleared)
	var ref map[string]any
	_ = json.Unmarshal([]byte(`{"by_issue":31,"by_project_id":23}`), &ref)
	var attached map[string]any
	_ = json.Unmarshal([]byte(`{"attachment":{"id":12,"filename":"shot.png"}}`), &attached)
	var resolved map[string]any
	_ = json.Unmarshal([]byte(`{"paths":["design.md"],"comment_ids":[482,483]}`), &resolved)
	entries := []entry{
		ev("block_added", payload), ev("block_added", blocks), ev("block_removed", payload), ev("block_cleared", cleared), ev("block_reblocked", cleared),
		ev("referenced", ref), ev("attachment_added", attached), ev("spec_comments_resolved", resolved),
		{Type: "comment", Kind: "me", Project: "web", IssueNumber: 5, IssueRef: "WEB-5", Author: &user{Login: "alice"}, Body: "is reviewing", agent: "neptune"},
	}
	known := facts{slugs: map[string]map[int64]string{"neptune": {23: "api"}}}
	text := r.digest(entries, known)[0].PlainText()
	want := "WEB-5\n⛔ bob marked it blocked by #77\n⛔ bob marked it blocking api#9\n⛔ bob removed its block by #77\n🟢 bob cleared its blocker #77\n" +
		"⛔ bob made its blocker #77 active again\n🔗 bob mentioned it in api#31\n📎 bob attached shot.png\n📝 bob resolved 2 spec comments\n💭 alice is reviewing"
	if text != want {
		t.Fatalf("text:\n%s\nwant:\n%s", text, want)
	}
}

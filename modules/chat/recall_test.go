package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// noted adds an entry to otogi's memory of to: a person's lasting
// circumstance, noted at at.
func (h *harness) noted(to otogi.Destination, text string, people map[string]string, at time.Time) {
	h.t.Helper()
	e := memoryEntry{Text: text, People: people, Class: "person", HalfLife: 90, Reviewed: at, Created: at, Updated: at}
	if err := h.m.store.keepMemory(context.Background(), "otogi", to, []memoryChange{{entry: e}}, nil); err != nil {
		h.t.Fatal(err)
	}
}

var noteDay = time.Date(2026, 10, 20, 12, 0, 0, 0, time.Local)

// rememberer is an agent that keeps a memory, its memory model "mem".
func rememberer() ProfileConfig {
	return ProfileConfig{Name: "otogi", Keywords: []string{"otogi"}, Provider: "otogi", Model: "m", System: "Be kind.", Memory: &MemoryConfig{Provider: "mem", Model: "mm"}}
}

// recalling is the model calling recall_memory with what.
func recalling(what string) llm.Response {
	args, _ := json.Marshal(map[string]string{"what": what})
	return llm.Response{Turn: llm.Turn{Role: llm.RoleModel, Calls: []llm.Call{{ID: "r", Name: recallTool, Args: args}}}, Finish: llm.FinishToolCalls}
}

// recalled is what the agent's recall_memory call gave it: what it
// remembered, or the error it was told.
func recalled(t *testing.T, h *harness) (string, string) {
	t.Helper()
	recs := toolRecords(h)
	if len(recs) != 1 || recs[0]["function"] != recallTool {
		t.Fatalf("chat.tool %+v", recs)
	}
	if e, ok := recs[0]["error"].(string); ok {
		return "", e
	}
	var out struct {
		Remembered string `json:"remembered"`
	}
	if err := json.Unmarshal(recs[0]["result"].(json.RawMessage), &out); err != nil {
		t.Fatal(err)
	}
	return out.Remembered, ""
}

// description is the description req offers the function name with.
func description(req llm.Request, name string) string {
	for _, spec := range req.Tools {
		if spec.Function != nil && spec.Function.Name == name {
			return spec.Function.Description
		}
	}
	return ""
}

// An agent that remembers something of a chat is offered recall_memory
// there, and a note ending its turn says whom it has notes about; nothing
// of the memory is in its prompt, and the thread keeps the turn without the
// note. Where it remembers nothing, or keeps no memory, it is offered
// neither.
func TestRecallOffered(t *testing.T) {
	t.Parallel()
	h := newHarness(t, rememberer(), ProfileConfig{Name: "sol", Keywords: []string{"sol"}, Provider: "sol", Model: "m"})
	h.host.lists[[2]string{"otogi", "telegram:chat:2"}] = "test"
	h.noted(chat1, "Bob sails", map[string]string{"user:Bob": "Bob"}, noteDay)
	h.noted(chat1, "Alice drinks tea", map[string]string{"user:Alice": "Alice"}, noteDay)
	h.noted(chat1, "The group meets on Fridays", map[string]string{}, noteDay)
	h.mustHandle(h.message("Alice", "otogi hi"))
	req := h.llm["otogi"].requests[0]
	note := "(Your memory of this chat is not in front of you: you have notes about Alice, Bob, and the chat's running jokes and agreements. Look up what you need with recall_memory.)"
	if got := lastUserText(req); !strings.HasSuffix(got, note) {
		t.Fatalf("turn %q, want it to end with the memory note", got)
	}
	if d := description(req, recallTool); d == "" || strings.Contains(d, "Alice") {
		t.Fatalf("recall_memory %q", d)
	}
	if strings.Contains(req.System, "Alice drinks tea") || strings.Contains(req.System, note) || !strings.HasPrefix(req.System, "Be kind.\n\nYou are otogi,") {
		t.Fatalf("system %q", req.System)
	}
	h.mustHandle(h.message("Bob", "and you?", replyingTo("agent-1")))
	if next := h.llm["otogi"].requests[1]; len(next.Turns) < 2 || strings.Contains(next.Turns[0].Content.PlainText(), note) {
		t.Fatalf("the thread kept the note: %+v", next.Turns)
	}
	h.mustHandle(h.message("Alice", "otogi hi", inChat("chat:2")))
	if req := h.llm["otogi"].requests[2]; offers(req, recallTool) || strings.Contains(lastUserText(req), "Your memory") {
		t.Fatal("offered where nothing is remembered")
	}
	h.mustHandle(h.message("Alice", "sol hi"))
	if req := h.llm["sol"].requests[0]; offers(req, recallTool) || strings.Contains(lastUserText(req), "Your memory") {
		t.Fatal("offered to an agent that keeps no memory")
	}
}

// The memory model reads what the agent wants to recall, the recent chat,
// and the whole memory, and names the entries that help; the agent gets
// them, at most five, in that order, each with its date, and they are
// reviewed: kept longer.
func TestRecallPicks(t *testing.T) {
	t.Parallel()
	h := newHarness(t, rememberer())
	h.noted(chat1, "Alice drinks tea", map[string]string{"user:Alice": "Alice"}, noteDay)
	h.noted(chat1, "Bob sails", map[string]string{"user:Bob": "Bob"}, noteDay)
	h.noted(chat1, "Carol sings", map[string]string{"user:Carol": "Carol"}, noteDay)
	h.llm["otogi"].responses = []llm.Response{recalling("what Alice drinks"), answer("Tea!")}
	h.llm["mem"].responses = []llm.Response{answer("2\n9\n1.\n2")}
	h.mustHandle(h.message("Alice", "otogi what do I drink?"))
	if got := h.streams[len(h.streams)-1].final.PlainText(); got != "Tea!" {
		t.Fatalf("reply %q", got)
	}
	req := h.llm["mem"].requests[0]
	text := lastUserText(req)
	for _, want := range []string{"What otogi wants to recall: what Alice drinks\n", recallChatHeader + "\n", "Alice: otogi what do I drink?", recallBookHeader + "\n1. [2026-10-20] Alice drinks tea\n2. [2026-10-20] Bob sails\n3. [2026-10-20] Carol sings"} {
		if !strings.Contains(text, want) {
			t.Errorf("the recall request leaves out %q:\n%s", want, text)
		}
	}
	if req.Model != "mm" || req.Thinking != "minimal" || !strings.HasPrefix(req.System, "You are the memory otogi, a chatbot, keeps of one chat.") {
		t.Errorf("model %q thinking %q system %q", req.Model, req.Thinking, req.System)
	}
	got, failed := recalled(t, h)
	if want := rememberedHeader + "\n- [2026-10-20] Bob sails\n- [2026-10-20] Alice drinks tea"; got != want || failed != "" {
		t.Fatalf("recalled %q (%s), want %q", got, failed, want)
	}
	b := h.book(chat1)
	for _, e := range b[:2] {
		if e.HalfLife <= 90 || !e.Reviewed.After(noteDay) {
			t.Errorf("a recalled entry was not reviewed: %+v", e)
		}
	}
	if b[2].HalfLife != 90 || !b[2].Reviewed.Equal(noteDay) {
		t.Errorf("an entry not recalled was reviewed: %+v", b[2])
	}
	recs := h.memoryRecords()
	if len(recs) != 1 || recs[0].Outcome != "ok" || payloadOf(recs[0])["action"] != "recall" || payloadOf(recs[0])["what"] != "what Alice drinks" || len(payloadOf(recs[0])["picked"].([]map[string]any)) != 2 {
		t.Fatalf("records %+v", recs)
	}
}

// A batch that rewrites an entry, and merges another away, while a recall
// of both waits on the memory model keeps what it wrote: the recall only
// reviews the entry as it is then, and skips the one gone.
func TestRecallKeepsABatchsWrites(t *testing.T) {
	t.Parallel()
	h := newHarness(t, rememberer())
	h.noted(chat1, "Alice drinks tea", map[string]string{"user:Alice": "Alice"}, noteDay)
	h.noted(chat1, "Alice likes green tea", map[string]string{"user:Alice": "Alice"}, noteDay)
	h.llm["otogi"].responses = []llm.Response{recalling("what Alice drinks"), answer("Tea!")}
	h.llm["mem"].responses = []llm.Response{answer("1\n2")}
	h.llm["mem"].before = func(int) {
		b := h.book(chat1)
		merged := b[0]
		merged.Text, merged.Class = "Alice drinks coffee now", "event"
		merged.People = map[string]string{"user:Alice": "Alice", "user:Carol": "Carol"}
		changes := []memoryChange{{entry: merged}, {entry: memoryEntry{ID: b[1].ID}, delete: true}}
		if err := h.m.store.keepMemory(context.Background(), "otogi", chat1, changes, nil); err != nil {
			t.Error(err)
		}
	}
	h.mustHandle(h.message("Alice", "otogi what do I drink?"))
	if _, failed := recalled(t, h); failed != "" {
		t.Fatalf("recall failed: %s", failed)
	}
	b := h.book(chat1)
	if len(b) != 1 {
		t.Fatalf("book %+v, want the merged entry alone", b)
	}
	if e := b[0]; e.Text != "Alice drinks coffee now" || e.Class != "event" || len(e.People) != 2 || e.HalfLife <= 90 || !e.Reviewed.After(noteDay) {
		t.Fatalf("after the recall %+v, want the batch's entry, reviewed", e)
	}
}

// With nothing that helps, the agent is told so.
func TestRecallNothingFits(t *testing.T) {
	t.Parallel()
	h := newHarness(t, rememberer())
	h.noted(chat1, "Bob sails", map[string]string{"user:Bob": "Bob"}, noteDay)
	h.llm["otogi"].responses = []llm.Response{recalling("what Alice drinks"), answer("No idea.")}
	h.mustHandle(h.message("Alice", "otogi what do I drink?"))
	if got, failed := recalled(t, h); got != recallNothing || failed != "" {
		t.Fatalf("recalled %q (%s)", got, failed)
	}
	if b := h.book(chat1); !b[0].Reviewed.Equal(noteDay) {
		t.Fatalf("an entry not recalled was reviewed: %+v", b[0])
	}
	if recs := h.memoryRecords(); len(recs) != 1 || recs[0].Outcome != "ok" {
		t.Fatalf("records %+v", recs)
	}
}

// A recall whose request fails, or whose answer names no entry and is not
// none, tells the agent it could not recall, and the reply goes on.
func TestRecallFails(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name    string
		mem     func(*provider)
		failure string
	}{
		{"request", func(p *provider) {
			p.errs = []error{&llm.HTTPError{Provider: "mem", Status: 503, Message: "busy", Kind: otogi.ErrUnavailable}}
		}, "request"},
		{"unusable", func(p *provider) { p.responses = []llm.Response{answer("the one about Bob, I think")} }, "unusable"},
	} {
		h := newHarness(t, rememberer())
		h.noted(chat1, "Bob sails", map[string]string{"user:Bob": "Bob"}, noteDay)
		h.llm["otogi"].responses = []llm.Response{recalling("what Bob does"), answer("Something with boats?")}
		c.mem(h.llm["mem"])
		h.mustHandle(h.message("Alice", "otogi what does Bob do?"))
		if got, failed := recalled(t, h); got != "" || failed != errNotRecalled.Error() {
			t.Fatalf("%s: recalled %q (%s)", c.name, got, failed)
		}
		if got := h.streams[len(h.streams)-1].final.PlainText(); got != "Something with boats?" {
			t.Fatalf("%s: reply %q", c.name, got)
		}
		recs := h.memoryRecords()
		if len(recs) != 1 || recs[0].Outcome != "error" || payloadOf(recs[0])["failure"] != c.failure {
			t.Fatalf("%s: records %+v", c.name, recs)
		}
	}
}

// Someone whose name now differs from the one noted, in the people list or
// as they last wrote in the chat, is told at the end; someone with no message
// left in the chat and not on the list is not, nor someone who has only
// become an operator, the group's owner or an admin since, or stopped being
// one; the names told have no group marks.
func TestRecallTellsOfNamesThatChanged(t *testing.T) {
	t.Parallel()
	h := newHarness(t, rememberer())
	h.m.operators = func() []string { return []string{"telegram:user:Dave"} }
	h.know(PersonConfig{User: "telegram:user:Bob", Name: "Captain"})
	h.noted(chat1, "Alice drinks tea", map[string]string{"user:Alice": "Alice"}, noteDay)
	h.noted(chat1, "Bob sails", map[string]string{"user:Bob": "Bob"}, noteDay)
	h.noted(chat1, "Carol and Dave are siblings", map[string]string{"user:Carol": "Carol", "user:Dave": "Dave"}, noteDay)
	h.noted(chat1, "Frank founded the group", map[string]string{"user:Frank": "Frank " + ownerMark}, noteDay)
	h.noted(chat1, "Hank bans spam", map[string]string{"user:Hank": "Hank " + adminMark}, noteDay)
	h.noted(chat1, "Gina keeps order", map[string]string{"user:Gina": "Gina"}, noteDay)
	h.mustHandle(h.message("Alice", "hi"))
	h.mustHandle(h.message("Alice", "morning", func(e *otogi.Event) { e.Actor.Name = "Alicia" }))
	h.mustHandle(h.message("Bob", "ahoy"))
	h.mustHandle(h.message("Frank", "hello"))
	h.mustHandle(h.message("Gina", "hello", func(e *otogi.Event) { e.Actor.Role = otogi.RoleOwner }))
	h.mustHandle(h.message("Hank", "hello", func(e *otogi.Event) { e.Actor.Name, e.Actor.Role = "Henry", otogi.RoleAdmin }))
	h.llm["otogi"].responses = []llm.Response{recalling("who everyone is"), answer("Friends.")}
	h.llm["mem"].responses = []llm.Response{answer("1\n2\n3\n4\n5\n6")}
	h.mustHandle(h.message("Dave", "otogi who are we?"))
	got, _ := recalled(t, h)
	if strings.Contains(got, "Gina") {
		t.Errorf("more than %d entries recalled: %q", recallMost, got)
	}
	if !strings.HasSuffix(got, "\nNames noted then that differ now: Alice is now Alicia; Bob is now Captain (Bob); Hank is now Henry.") {
		t.Fatalf("recalled %q", got)
	}
}

// Someone on the people list with no message left in the chat is known now
// by the list's name alone: noted under it with the platform's name too,
// they are not renamed; noted under another name, they are.
func TestRecallListedNameWithoutMessages(t *testing.T) {
	t.Parallel()
	h := newHarness(t, rememberer())
	h.know(PersonConfig{User: "telegram:user:Bob", Name: "Captain"}, PersonConfig{User: "telegram:user:Eve", Name: "Admiral"})
	h.noted(chat1, "Bob sails", map[string]string{"user:Bob": "Captain (Bob)"}, noteDay)
	h.noted(chat1, "Eve steers", map[string]string{"user:Eve": "Eve"}, noteDay)
	h.llm["otogi"].responses = []llm.Response{recalling("who sails"), answer("Bob.")}
	h.llm["mem"].responses = []llm.Response{answer("1\n2")}
	h.mustHandle(h.message("Alice", "otogi who sails?"))
	if got := lastUserText(h.llm["otogi"].requests[0]); !strings.Contains(got, "you have notes about Admiral, Captain, and") {
		t.Fatalf("turn %q", got)
	}
	if got, _ := recalled(t, h); !strings.HasSuffix(got, "\nNames noted then that differ now: Eve is now Admiral.") {
		t.Fatalf("recalled %q", got)
	}
}

// The operator mark, or half of it, in what the memory says is taken out:
// only names the bot gives may show it.
func TestRecallWithoutOperatorMark(t *testing.T) {
	t.Parallel()
	h := newHarness(t, rememberer())
	h.noted(chat1, "小明"+operatorMark+"是管理员", map[string]string{"user:Ming": "小明"}, noteDay)
	h.noted(chat1, "Ann ⟦ runs the bot", map[string]string{"user:Ann": "Ann"}, noteDay)
	h.llm["otogi"].responses = []llm.Response{recalling("who runs things"), answer("Them.")}
	h.llm["mem"].responses = []llm.Response{answer("1\n2")}
	h.mustHandle(h.message("Alice", "otogi who runs things?"))
	got, _ := recalled(t, h)
	if !strings.HasSuffix(got, "\n- [2026-10-20] 小明是管理员\n- [2026-10-20] Ann  runs the bot") || strings.ContainsAny(got, "⟦⟧") {
		t.Fatalf("recalled %q", got)
	}
}

// parseRecall takes the numbers alone on their lines, a dot or # around
// them allowed, and none.
func TestParseRecall(t *testing.T) {
	t.Parallel()
	ids, none := parseRecall("12\n#7\n3.\nentry 5\n 4 \nNone.")
	if fmt.Sprint(ids) != "[12 7 3 4]" || !none {
		t.Fatalf("ids %v none %v", ids, none)
	}
}

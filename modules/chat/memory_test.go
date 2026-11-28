package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

var chat1 = otogi.Destination{Platform: "telegram", Account: "main", Conversation: "chat:1"}

// newMemoryHarness starts one agent that sees context messages and keeps
// a memory with mc, its model answering with the given texts in turn.
func newMemoryHarness(t *testing.T, mc MemoryConfig, context int, answers ...string) *harness {
	t.Helper()
	h := newHarness(t, ProfileConfig{Name: "otogi", Keywords: []string{"otogi"}, Provider: "otogi", Model: "m", Context: ptr(context), Memory: &mc})
	mem := h.llm[mc.Provider]
	if mem == nil {
		t.Fatalf("no provider %q", mc.Provider)
	}
	mem.responses = nil
	for _, a := range answers {
		mem.responses = append(mem.responses, answer(a))
	}
	return h
}

// chatter has who say n numbered lines in chat:1, prefixed.
func (h *harness) chatter(who, prefix string, n int, mods ...func(*otogi.Event)) {
	h.t.Helper()
	for i := 1; i <= n; i++ {
		h.mustHandle(h.message(who, fmt.Sprintf("%s%d", prefix, i), mods...))
	}
}

func inChat(id otogi.ConversationID) func(*otogi.Event) {
	return func(e *otogi.Event) { e.Conversation.ID = id }
}

func (h *harness) remember() {
	h.t.Helper()
	if err := h.m.remember(context.Background(), nil); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) book(to otogi.Destination) []memoryEntry {
	h.t.Helper()
	b, err := h.m.store.book(context.Background(), "otogi", to)
	if err != nil {
		h.t.Fatal(err)
	}
	return b
}

func (h *harness) memoryRecords() []otogi.Record {
	h.host.mu.Lock()
	defer h.host.mu.Unlock()
	var out []otogi.Record
	for _, r := range h.host.records {
		if r.Kind == "chat.memory" {
			out = append(out, r)
		}
	}
	return out
}

func payloadOf(r otogi.Record) map[string]any {
	p, _ := r.Payload.(map[string]any)
	return p
}

// section is what a memory request says under header, up to the next
// header.
func section(req llm.Request, header string) string {
	text := lastUserText(req)
	_, after, ok := strings.Cut(text, header)
	if !ok {
		return ""
	}
	for _, next := range []string{memoryNowHeader, memoryBeforeHeader, memoryBatchHeader, memoryAfterHeader} {
		if i := strings.Index(after, next); i >= 0 {
			after = after[:i]
		}
	}
	return after
}

func TestMemoryTakesWhatTheAgentNoLongerSees(t *testing.T) {
	h := newMemoryHarness(t, MemoryConfig{Provider: "mem", Model: "mm", Batch: 20}, 5, "+ p1 | person | Alice drinks tea every morning")
	mem := h.llm["mem"]
	h.chatter("Alice", "line ", 24)
	h.remember()
	if len(mem.requests) != 0 {
		t.Fatalf("19 messages out of sight were taken: %d requests", len(mem.requests))
	}
	h.chatter("Bob", "more ", 1)
	h.remember()
	if len(mem.requests) != 1 {
		t.Fatalf("requests %d, want 1", len(mem.requests))
	}
	req := mem.requests[0]
	if req.Model != "mm" || req.Thinking != "minimal" {
		t.Errorf("model %q thinking %q, want mm and minimal", req.Model, req.Thinking)
	}
	batch, later := section(req, memoryBatchHeader), section(req, memoryAfterHeader)
	if !strings.Contains(batch, "line 1\n") || !strings.Contains(batch, "line 20") || strings.Contains(batch, "line 21") {
		t.Errorf("batch is not lines 1 to 20:\n%s", batch)
	}
	if !strings.Contains(later, "line 21") || !strings.Contains(later, "more 1") {
		t.Errorf("later messages are not what the agent still sees:\n%s", later)
	}
	if !strings.Contains(batch, "Alice (p1)") {
		t.Errorf("senders are not tagged:\n%s", batch)
	}
	b := h.book(chat1)
	if len(b) != 1 || b[0].Text != "Alice drinks tea every morning" || b[0].People["user:Alice"] != "Alice" {
		t.Fatalf("book %+v", b)
	}
	recs := h.memoryRecords()
	if len(recs) != 1 || recs[0].Outcome != "ok" || payloadOf(recs[0])["action"] != "consolidate" || !strings.Contains(fmt.Sprint(payloadOf(recs[0])["added"]), "Alice drinks tea") {
		t.Fatalf("records %+v", recs)
	}
	h.remember()
	if len(mem.requests) != 1 {
		t.Fatalf("the same messages were taken again: %d requests", len(mem.requests))
	}
}

func TestMemoryTakesWhatIsAboutToBePruned(t *testing.T) {
	h := newMemoryHarness(t, MemoryConfig{Provider: "mem", Model: "mm", Batch: 20}, 5, "none")
	mem := h.llm["mem"]
	h.chatter("Alice", "line ", 3)
	h.remember()
	if len(mem.requests) != 0 {
		t.Fatalf("messages in sight were taken: %d requests", len(mem.requests))
	}
	h.now = h.now.Add(30*24*time.Hour - 12*time.Hour)
	h.remember()
	if len(mem.requests) != 1 || !strings.Contains(section(mem.requests[0], memoryBatchHeader), "line 3") {
		t.Fatalf("messages about to be pruned were not taken: %d requests", len(mem.requests))
	}
	if len(h.book(chat1)) != 0 {
		t.Error("none changed the book")
	}
}

// The agent's own lines reach its memory as said shows them: by its title,
// without the label its reply showed first, as the platform echoed it.
func TestMemoryReadsTheAgentsLinesAsSaid(t *testing.T) {
	mc := MemoryConfig{Provider: "mem", Model: "mm", Batch: 20}
	h := newHarness(t, ProfileConfig{Name: "otogi", Label: "Otogi", Keywords: []string{"otogi"}, Provider: "otogi", Model: "m", Context: ptr(5), Memory: &mc})
	mem := h.llm["mem"]
	mem.responses = []llm.Response{answer("none")}
	h.mustHandle(h.message("Alice", "otogi hi"))
	said := h.streams[0]
	if got := said.final.PlainText(); got != "Otogi\nhello" {
		t.Fatalf("sent %q", got)
	}
	h.echo(said)
	h.chatter("Bob", "line ", 23)
	h.remember()
	if len(mem.requests) != 1 {
		t.Fatalf("requests %d, want 1", len(mem.requests))
	}
	batch := section(mem.requests[0], memoryBatchHeader)
	if !strings.Contains(batch, "Otogi (this account): hello") || strings.Contains(batch, "**") {
		t.Fatalf("the agent's line in the batch:\n%s", batch)
	}
	if !strings.Contains(mem.requests[0].System, "Lines from Otogi (this account) are what Otogi said.") {
		t.Fatalf("system %q", mem.requests[0].System)
	}
}

func TestMemoryOnlyInTheAgentsChats(t *testing.T) {
	h := newHarness(t,
		ProfileConfig{Name: "otogi", Keywords: []string{"otogi"}, Provider: "otogi", Model: "m", Context: ptr(0), Memory: &MemoryConfig{Provider: "mem", Model: "mm", Batch: 20}},
		ProfileConfig{Name: "other", Keywords: []string{"other"}, Provider: "other", Model: "m"})
	h.host.lists[[2]string{"other", "telegram:chat:2"}] = "test"
	h.chatter("Alice", "elsewhere ", 25, inChat("chat:2"))
	h.remember()
	if n := len(h.llm["mem"].requests); n != 0 {
		t.Fatalf("a chat the agent does not serve was taken: %d requests", n)
	}
}

func TestMemoryLeavesOutDeletedMessages(t *testing.T) {
	h := newMemoryHarness(t, MemoryConfig{Provider: "mem", Model: "mm", Batch: 20}, 0, "none")
	h.chatter("Alice", "line ", 20)
	h.mustHandle(otogi.Event{Platform: "telegram", Account: "main", Conversation: otogi.Conversation{ID: "chat:1", Kind: otogi.ConversationGroup}, SourceID: "m3", Action: otogi.ActionMessageDeleted})
	h.chatter("Alice", "again ", 1)
	h.remember()
	batch := section(h.llm["mem"].requests[0], memoryBatchHeader)
	if strings.Contains(batch, "line 3\n") || !strings.Contains(batch, "again 1") {
		t.Fatalf("batch:\n%s", batch)
	}
}

func TestMemoryOperations(t *testing.T) {
	h := newMemoryHarness(t, MemoryConfig{Provider: "mem", Model: "mm", Batch: 20}, 0,
		"+ p1 | person | Alice drinks tea\n+ p1 p2 | Person | Alice and Bob share a cat",
		"~ 1 | Alice drinks green tea\n- 2\n- 99\n+ p9 | person | nobody\nsomething else\n+ p1 | hobby | Alice knits",
		"none")
	h.chatter("Alice", "a", 10)
	h.chatter("Bob", "b", 10)
	h.remember()
	b := h.book(chat1)
	if len(b) != 2 || b[1].People["user:Bob"] != "Bob" || b[1].People["user:Alice"] != "Alice" || b[1].Class != "person" || b[1].HalfLife != 90 || !b[1].Reviewed.Equal(h.now) {
		t.Fatalf("book %+v", b)
	}
	if !strings.Contains(section(h.llm["mem"].requests[0], memoryNowHeader), "(empty)") {
		t.Error("an empty book is not said to be empty")
	}
	h.chatter("Alice", "c", 20)
	h.remember()
	now := section(h.llm["mem"].requests[1], memoryNowHeader)
	if !strings.Contains(now, "1. [2026-") || !strings.Contains(now, ", person] Alice drinks tea") {
		t.Errorf("the book is not numbered in the request:\n%s", now)
	}
	b = h.book(chat1)
	if len(b) != 1 || b[0].Text != "Alice drinks green tea" {
		t.Fatalf("book %+v", b)
	}
	rec := h.memoryRecords()[1]
	for _, want := range []string{"Alice drinks tea", "Alice drinks green tea", "Alice and Bob share a cat", "- 99", "+ p9 | person | nobody", "something else", "not a change", "+ p1 | hobby | Alice knits", `no class "hobby"`} {
		if !strings.Contains(fmt.Sprint(rec.Payload), want) {
			t.Errorf("record leaves out %q: %v", want, rec.Payload)
		}
	}
	h.chatter("Alice", "d", 20)
	h.remember()
	if b := h.book(chat1); len(b) != 1 {
		t.Fatalf("none changed the book: %+v", b)
	}
	if recs := h.memoryRecords(); recs[2].Outcome != "ok" {
		t.Errorf("none recorded as %s", recs[2].Outcome)
	}
}

func TestMemoryRetriesWhenTheRequestFails(t *testing.T) {
	h := newMemoryHarness(t, MemoryConfig{Provider: "mem", Model: "mm", Batch: 20}, 0, "+ p1 | person | Alice drinks tea")
	mem := h.llm["mem"]
	mem.errs = []error{
		&llm.HTTPError{Provider: "mem", Status: 503, Message: "busy", Kind: otogi.ErrUnavailable},
		&llm.HTTPError{Provider: "mem", Status: 400, Message: "no such model", Kind: otogi.ErrInvalid},
		&llm.HTTPError{Provider: "mem", Status: 503, Message: "busy", Kind: otogi.ErrUnavailable},
		&llm.HTTPError{Provider: "mem", Status: 503, Message: "busy", Kind: otogi.ErrUnavailable},
	}
	h.chatter("Alice", "line ", 20)
	for range 4 {
		h.remember()
	}
	if len(mem.requests) != 4 {
		t.Fatalf("a failed request did not end the run: %d requests", len(mem.requests))
	}
	h.remember()
	if !strings.Contains(section(mem.requests[4], memoryBatchHeader), "line 1\n") {
		t.Fatal("after failed requests, the batch was not the same")
	}
	if b := h.book(chat1); len(b) != 1 {
		t.Fatalf("book %+v", b)
	}
	recs := h.memoryRecords()
	if len(recs) != 5 || recs[0].Outcome != "error" || payloadOf(recs[0])["failure"] != "request" || !strings.Contains(fmt.Sprint(payloadOf(recs[1])["error"]), "no such model") {
		t.Fatalf("records %+v", recs)
	}
}

func TestMemorySkipsABatchAnsweredUnusablyThreeTimes(t *testing.T) {
	h := newMemoryHarness(t, MemoryConfig{Provider: "mem", Model: "mm", Batch: 20}, 0, "nonsense", "more nonsense", "", "+ p1 | person | Alice drinks tea")
	mem := h.llm["mem"]
	mem.responses[2] = llm.Response{Finish: llm.FinishBlocked, Reason: "SAFETY"}
	h.chatter("Alice", "line ", 40)
	h.remember()
	if len(mem.requests) != 1 {
		t.Fatalf("an unusable answer was asked again in the same run: %d requests", len(mem.requests))
	}
	h.remember()
	h.remember()
	for i, req := range mem.requests[:3] {
		if !strings.Contains(section(req, memoryBatchHeader), "line 1\n") {
			t.Fatalf("request %d is not the first batch", i)
		}
	}
	if len(mem.requests) != 4 || !strings.Contains(section(mem.requests[3], memoryBatchHeader), "line 21") {
		t.Fatalf("after three unusable answers the next batch was not taken: %d requests", len(mem.requests))
	}
	recs := h.memoryRecords()
	if recs[0].Outcome != "error" || payloadOf(recs[0])["failure"] != "unusable" || recs[2].Outcome != "degraded" || payloadOf(recs[2])["skipped"] != true || recs[3].Outcome != "ok" {
		t.Fatalf("records %+v", recs)
	}
}

func TestMemoryKeepsTheBookAndItsMarkTogether(t *testing.T) {
	h := newMemoryHarness(t, MemoryConfig{Provider: "mem", Model: "mm", Batch: 20}, 0, "+ p1 | person | Alice drinks tea")
	h.chatter("Alice", "line ", 20)
	db := h.m.store.db
	for _, q := range []string{
		"CREATE TRIGGER no_mark_insert BEFORE INSERT ON memory_marks BEGIN SELECT RAISE(ABORT, 'no'); END",
		"CREATE TRIGGER no_mark_update BEFORE UPDATE ON memory_marks BEGIN SELECT RAISE(ABORT, 'no'); END",
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.m.remember(context.Background(), nil); err == nil {
		t.Fatal("no error when the mark could not be kept")
	}
	if b := h.book(chat1); len(b) != 0 {
		t.Fatalf("the book changed without its mark: %+v", b)
	}
	for _, q := range []string{"DROP TRIGGER no_mark_insert", "DROP TRIGGER no_mark_update"} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	h.remember()
	h.remember()
	if b := h.book(chat1); len(b) != 1 {
		t.Fatalf("book %+v", b)
	}
	if n := len(h.llm["mem"].requests); n != 2 {
		t.Fatalf("requests %d, want the batch twice", n)
	}
}

// The memory model is told the classes, and files each new entry under one.
func TestMemoryRequestNamesTheClasses(t *testing.T) {
	h := newMemoryHarness(t, MemoryConfig{Provider: "mem", Model: "mm", Batch: 20}, 0, "none")
	h.chatter("Alice", "line ", 20)
	h.remember()
	system := h.llm["mem"].requests[0].System
	for _, c := range memoryClasses {
		if !strings.Contains(system, "\n- "+c.name+": "+strings.ReplaceAll(c.about, "{agent}", "otogi")+"\n") {
			t.Errorf("the system prompt leaves out class %s:\n%s", c.name, system)
		}
	}
	if strings.Contains(system, "{") {
		t.Errorf("a placeholder is left:\n%s", system)
	}
}

// An entry fades by its class's half-life and is forgotten under a fifth;
// what is still clear is kept. The forget is a chat.memory record.
func TestMemoryForgetsWhatFaded(t *testing.T) {
	h := newMemoryHarness(t, MemoryConfig{Provider: "mem", Model: "mm", Batch: 20}, 0,
		"+ p1 | news | Alice says the rocket launched\n+ p1 | person | Alice lives in Osaka")
	h.chatter("Alice", "line ", 20)
	h.remember()
	if len(h.book(chat1)) != 2 {
		t.Fatalf("book %+v", h.book(chat1))
	}
	h.now = h.now.Add(4 * 24 * time.Hour) // news: a quarter left, over a fifth
	h.remember()
	if len(h.book(chat1)) != 2 {
		t.Fatalf("forgotten too soon: %+v", h.book(chat1))
	}
	h.now = h.now.Add(24 * time.Hour) // news: 0.18
	h.remember()
	b := h.book(chat1)
	if len(b) != 1 || b[0].Text != "Alice lives in Osaka" {
		t.Fatalf("book %+v", b)
	}
	recs := h.memoryRecords()
	rec := recs[len(recs)-1]
	gone, _ := payloadOf(rec)["deleted"].([]map[string]any)
	if payloadOf(rec)["action"] != "forget" || rec.Outcome != "ok" || len(gone) != 1 || gone[0]["text"] != "Alice says the rocket launched" || gone[0]["why"] != "faded" || gone[0]["clarity"] != 0.18 {
		t.Fatalf("record %+v", rec)
	}
}

// Brought up again, rewritten or merged by a batch, an entry is kept
// longer: its half-life grows the more it had faded.
func TestMemoryKeepsLongerWhatComesUpAgain(t *testing.T) {
	h := newMemoryHarness(t, MemoryConfig{Provider: "mem", Model: "mm", Batch: 20}, 0,
		"+ p1 | event | Alice is moving house\n+ p1 | event | Alice packs boxes\n+ p1 | person | Alice has a cat",
		"~ 1 | Alice moved house on 11-20",
		"= 2 3 | Alice has a cat, who hates the boxes")
	h.chatter("Alice", "a", 20)
	h.remember()
	h.now = h.now.Add(7 * 24 * time.Hour) // events at half, the 20 messages after aside
	h.chatter("Alice", "b", 20)
	h.remember()
	b := h.book(chat1)
	if b[0].Text != "Alice moved house on 11-20" || b[0].HalfLife < 17.4 || b[0].HalfLife > 17.6 || !b[0].Reviewed.Equal(h.now) {
		t.Fatalf("rewritten %+v", b[0])
	}
	h.chatter("Alice", "c", 20)
	h.remember()
	b = h.book(chat1)
	if len(b) != 2 || b[1].ID != 2 || b[1].Class != "person" || b[1].HalfLife <= 90 || !b[1].Reviewed.Equal(h.now) {
		t.Fatalf("merged %+v: it keeps the class of the entry that lasts longest, reviewed", b)
	}
	h.now = h.now.Add(13 * 24 * time.Hour) // an event left alone would be at 0.5^(20/7), gone; the rewritten one is at 0.5^(13/17.5)
	h.remember()
	if b := h.book(chat1); len(b) != 2 {
		t.Fatalf("an entry brought up again was forgotten: %+v", b)
	}
}

// A book over memoryMaxEntries loses those that would be forgotten soonest.
func TestMemoryKeepsAtMostItsLimit(t *testing.T) {
	h := newMemoryHarness(t, MemoryConfig{Provider: "mem", Model: "mm", Batch: 20}, 0, "none")
	var changes []memoryChange
	for i := range memoryMaxEntries + 2 {
		e := memoryEntry{Text: fmt.Sprintf("thing %d", i), People: map[string]string{}, Class: "person", HalfLife: 90, Reviewed: h.now, Created: h.now, Updated: h.now}
		if i < 2 {
			e.Class, e.HalfLife = "event", 7
		}
		changes = append(changes, memoryChange{entry: e})
	}
	if err := h.m.store.keepMemory(context.Background(), "otogi", chat1, changes, nil); err != nil {
		t.Fatal(err)
	}
	h.remember()
	b := h.book(chat1)
	if len(b) != memoryMaxEntries || b[0].Text != "thing 2" {
		t.Fatalf("%d entries, first %+v", len(b), b[0])
	}
	recs := h.memoryRecords()
	gone, _ := payloadOf(recs[len(recs)-1])["deleted"].([]map[string]any)
	if len(gone) != 2 || gone[0]["why"] != "over the limit" {
		t.Fatalf("record %+v", recs[len(recs)-1])
	}
}

func TestMemoryTakesAtMostThreeBatchesARunOldestFirst(t *testing.T) {
	h := newMemoryHarness(t, MemoryConfig{Provider: "mem", Model: "mm", Batch: 20}, 0, "none")
	h.host.lists[[2]string{"otogi", "telegram:chat:2"}] = "test"
	h.chatter("Alice", "two-", 40, inChat("chat:2"))
	h.chatter("Alice", "one-", 40)
	mem := h.llm["mem"]
	h.remember()
	if len(mem.requests) != 3 {
		t.Fatalf("requests %d, want 3", len(mem.requests))
	}
	for i, want := range []string{"two-1\n", "two-21", "one-1\n"} {
		if !strings.Contains(section(mem.requests[i], memoryBatchHeader), want) {
			t.Errorf("request %d is not the batch from %s", i, want)
		}
	}
	h.remember()
	if len(mem.requests) != 4 || !strings.Contains(section(mem.requests[3], memoryBatchHeader), "one-21") {
		t.Fatalf("the next run did not take the rest: %d requests", len(mem.requests))
	}
}

func TestMemoryConfig(t *testing.T) {
	t.Parallel()
	providers := func(name string) (llm.Provider, error) {
		if name == "gemini" || name == "cheap" {
			return &provider{}, nil
		}
		return nil, errors.New("not configured")
	}
	profile := func(mc MemoryConfig) []ProfileConfig {
		return []ProfileConfig{{Name: "a", Keywords: []string{"a"}, Provider: "gemini", Model: "m", Memory: &mc}}
	}
	m, err := New(Config{Profiles: profile(MemoryConfig{})}, providers, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := m.profiles[0].memory
	if got == nil || got.model != "m" || got.thinking != "minimal" || got.batch != 100 || got.provider == nil {
		t.Fatalf("defaults %+v", got)
	}
	if m, err := New(Config{Profiles: profile(MemoryConfig{Provider: "cheap", Model: "c", Thinking: "low", Batch: 50})}, providers, nil); err != nil || m.profiles[0].memory.model != "c" || m.profiles[0].memory.batch != 50 {
		t.Fatalf("set: %v", err)
	}
	for name, c := range map[string]struct {
		mc   MemoryConfig
		path string
	}{
		"small batch":           {MemoryConfig{Batch: 10}, "profiles[0].memory.batch"},
		"no such provider":      {MemoryConfig{Provider: "nope", Model: "x"}, "profiles[0].memory.provider"},
		"provider and no model": {MemoryConfig{Provider: "cheap"}, "profiles[0].memory.model"},
		"bad thinking":          {MemoryConfig{Thinking: "max"}, "profiles[0].memory.thinking"},
	} {
		_, err := New(Config{Profiles: profile(c.mc)}, providers, nil)
		var se *otogi.SettingError
		if !errors.As(err, &se) || se.Path != c.path {
			t.Errorf("%s: err %v, want a refusal at %s", name, err, c.path)
		}
	}
}

// A book whose request always fails, such as a batch the service refuses as
// too long, holds back no other: it is set aside for the run, takes none of
// its batches, and is asked again next run.
func TestMemoryAFailingBookHoldsBackNoOther(t *testing.T) {
	h := newMemoryHarness(t, MemoryConfig{Provider: "mem", Model: "mm", Batch: 20}, 0, "none")
	h.host.lists[[2]string{"otogi", "telegram:chat:2"}] = "test"
	mem := h.llm["mem"]
	mem.errFor = func(req llm.Request) error {
		if strings.Contains(section(req, memoryBatchHeader), "two-") {
			return &llm.HTTPError{Provider: "mem", Status: 400, Message: "too long", Kind: otogi.ErrInvalid}
		}
		return nil
	}
	h.chatter("Alice", "two-", 20, inChat("chat:2"))
	h.chatter("Alice", "one-", 80)
	h.remember()
	var one, two int
	for _, req := range mem.requests {
		if strings.Contains(section(req, memoryBatchHeader), "two-") {
			two++
		} else {
			one++
		}
	}
	if two != 1 || one != 3 {
		t.Fatalf("one run: %d requests for the failing book, %d for the other, want 1 and 3", two, one)
	}
	h.remember()
	if !strings.Contains(section(mem.requests[len(mem.requests)-1], memoryBatchHeader), "one-61") || !strings.Contains(section(mem.requests[len(mem.requests)-2], memoryBatchHeader), "two-1\n") {
		t.Fatal("the next run did not ask the failing book again and go on with the other")
	}
}

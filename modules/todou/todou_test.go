package todou

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/modulestore"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// fakeCLI answers CLI calls; watchOutputs are consumed one per watch call.
type fakeCLI struct {
	calls        [][]string
	watchOutputs []string
	watchErr     error
	projects     []string
}

func (f *fakeCLI) run(_ context.Context, args ...string) ([]byte, error) {
	f.calls = append(f.calls, args)
	switch {
	case args[0] == "watch" && slices.Contains(args, "--print-cursor"):
		return []byte("c0\n"), nil
	case args[0] == "watch":
		if f.watchErr != nil {
			return nil, f.watchErr
		}
		out := f.watchOutputs[0]
		f.watchOutputs = f.watchOutputs[1:]
		return []byte(out), nil
	case args[0] == "issue":
		return []byte(fmt.Sprintf(`{"title":"Title of %s","body":"Body of %s"}`, args[2], args[2])), nil
	case args[0] == "project":
		out, _ := json.Marshal(func() []map[string]string {
			var ps []map[string]string
			for _, p := range f.projects {
				ps = append(ps, map[string]string{"slug": p})
			}
			return ps
		}())
		return out, nil
	case args[0] == "whoami":
		return []byte(`{"login":"bot","display_name":"Notifier","kind":"machine"}`), nil
	}
	return nil, fmt.Errorf("unexpected call %v", args)
}

func (f *fakeCLI) sinces() []string {
	var out []string
	for _, c := range f.calls {
		if i := slices.Index(c, "--since"); i >= 0 {
			out = append(out, c[i+1])
		}
	}
	return out
}

type sent struct {
	origin, slot string
	to           otogi.Destination
	msg          otogi.Message
}

// fakeInvocation records sends; fail decides whether a send to a
// conversation fails.
type fakeInvocation struct {
	otogi.Invocation
	host   *fakeHost
	origin string
	sends  *[]sent
	fail   func(otogi.ConversationID) error
}

func (i *fakeInvocation) Host() otogi.Host { return i.host }
func (i *fakeInvocation) WithOrigin(id string) otogi.Invocation {
	c := *i
	c.origin = id
	return &c
}
func (i *fakeInvocation) SendTo(_ context.Context, slot string, to otogi.Destination, m otogi.Message) (otogi.EventRef, error) {
	if i.fail != nil {
		if err := i.fail(to.Conversation); err != nil {
			return otogi.EventRef{}, err
		}
	}
	*i.sends = append(*i.sends, sent{origin: i.origin, slot: slot, to: to, msg: m})
	return otogi.EventRef{}, nil
}
func (i *fakeInvocation) Send(ctx context.Context, slot string, m otogi.Message) (otogi.EventRef, error) {
	return i.SendTo(ctx, slot, otogi.Destination{Conversation: "reply"}, m)
}

type fakeHost struct {
	otogi.Host
	storage *modulestore.Storage
	records []otogi.Record
	job     otogi.Job
}

func (h *fakeHost) Logger() *slog.Logger { return slog.New(slog.DiscardHandler) }
func (h *fakeHost) Database(ctx context.Context) (*sql.DB, error) {
	return h.storage.Database(ctx, "todou")
}
func (h *fakeHost) Record(_ context.Context, r otogi.Record) { h.records = append(h.records, r) }
func (h *fakeHost) Schedule(_, _ string, job otogi.Job) error {
	h.job = job
	return nil
}

type harness struct {
	m     *Module
	cli   *fakeCLI
	host  *fakeHost
	sends []sent
	fail  func(otogi.ConversationID) error
}

func newHarness(t *testing.T, cfg Config) *harness {
	t.Helper()
	ctx := context.Background()
	storage, err := modulestore.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	cfg.CLI, cfg.Server, cfg.Token = "todou", "https://todou.test", "todou_pat_x"
	m, err := New(cfg, []string{"telegram:user:1"})
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{m: m, cli: &fakeCLI{projects: []string{"web", "api"}}, host: &fakeHost{storage: storage}}
	m.cli = client{run: h.cli.run}
	if err := m.Start(ctx, h.host); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *harness) inv() *fakeInvocation {
	return &fakeInvocation{host: h.host, origin: "job", sends: &h.sends, fail: h.fail}
}

func (h *harness) poll(t *testing.T) error {
	t.Helper()
	return h.host.job(context.Background(), h.inv())
}

func (h *harness) command(t *testing.T, conversation otogi.ConversationID, actor otogi.ActorID, direction otogi.Direction, args string) string {
	t.Helper()
	e := otogi.Event{Platform: "telegram", Account: "main", Direction: direction, Conversation: otogi.Conversation{ID: conversation}, Actor: otogi.Actor{ID: actor}, Command: &otogi.Command{Name: "todou", Args: args}}
	before := len(h.sends)
	if err := h.m.Handle(context.Background(), e, h.inv()); err != nil {
		t.Fatal(err)
	}
	return h.sends[before].msg.PlainText()
}

func record(v map[string]any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func comment(id int64, project string, issue int64, body string) string {
	return record(map[string]any{"type": "comment", "id": id, "project": project, "issue_number": issue, "issue_ref": fmt.Sprintf("%s#%d", project, issue),
		"author": map[string]any{"login": "alice", "display_name": "Alice"}, "body": body, "created_at": "2026-10-16T00:00:00Z"})
}

func cursor(c string) string { return record(map[string]any{"type": "cursor", "next_cursor": c}) }

func lines(records ...string) string { return strings.Join(records, "\n") + "\n" }

func TestFirstPollStartsAtTheCurrentPosition(t *testing.T) {
	h := newHarness(t, Config{})
	h.cli.watchOutputs = []string{lines(cursor("c1"))}
	if err := h.poll(t); err != nil {
		t.Fatal(err)
	}
	if err := h.poll(t); err != nil {
		t.Fatal(err)
	}
	if got := h.cli.sinces(); !slices.Equal(got, []string{"c0"}) {
		t.Fatalf("watch --since = %v, want the bootstrapped cursor", got)
	}
	if len(h.sends) != 0 {
		t.Fatalf("bootstrap sent %d messages", len(h.sends))
	}
}

func TestPollNotifiesLinkedChatsInOrder(t *testing.T) {
	h := newHarness(t, Config{})
	h.command(t, "chat:a", "", otogi.DirectionOutgoing, "link web") // the owner
	h.command(t, "chat:b", "user:1", otogi.DirectionIncoming, "link web")
	h.command(t, "chat:b", "user:1", otogi.DirectionIncoming, "link api")
	h.sends = nil
	_ = h.poll(t) // bootstrap
	h.cli.watchOutputs = []string{
		lines(comment(1, "web", 42, "first"), comment(2, "api", 7, "second"), comment(3, "other", 1, "unlinked"), cursor("c1"),
			comment(4, "web", 42, "after the last cursor: replayed next time")),
		lines(cursor("c2")),
	}
	if err := h.poll(t); err != nil {
		t.Fatal(err)
	}
	if err := h.poll(t); err != nil {
		t.Fatal(err)
	}
	if got := h.cli.sinces(); !slices.Equal(got, []string{"c0", "c1"}) {
		t.Fatalf("cursors = %v", got)
	}
	byChat := map[otogi.ConversationID]string{}
	for _, s := range h.sends {
		byChat[s.to.Conversation] += s.msg.PlainText()
		if !strings.HasPrefix(s.origin, "outbox:") || s.slot != "notify" {
			t.Errorf("send keyed by %q/%q, want the outbox row", s.origin, s.slot)
		}
	}
	a, b := byChat["chat:a"], byChat["chat:b"]
	if !strings.Contains(a, "first") || strings.Contains(a, "second") || strings.Contains(a+b, "unlinked") || strings.Contains(a+b, "replayed") {
		t.Fatalf("chat a = %q, chat b = %q", a, b)
	}
	if strings.Index(b, "first") > strings.Index(b, "second") || !strings.Contains(b, "“Title of web/42”") {
		t.Fatalf("chat b = %q", b)
	}
}

func TestFailuresAreRetriedPerChatThenGivenUp(t *testing.T) {
	h := newHarness(t, Config{MaxAttempts: 2})
	h.command(t, "chat:a", "", otogi.DirectionOutgoing, "link web")
	h.command(t, "chat:b", "", otogi.DirectionOutgoing, "link web")
	h.sends = nil
	_ = h.poll(t)
	h.fail = func(c otogi.ConversationID) error {
		if c == "chat:a" {
			return errors.New("CHAT_WRITE_FORBIDDEN")
		}
		return nil
	}
	h.cli.watchOutputs = []string{lines(comment(1, "web", 1, "x"), cursor("c1")), lines(cursor("c2")), lines(cursor("c3"))}
	_ = h.poll(t)
	if len(h.sends) != 1 || h.sends[0].to.Conversation != "chat:b" {
		t.Fatalf("a failing chat blocked another: %+v", h.sends)
	}
	_ = h.poll(t) // second attempt fails too: given up
	if len(h.host.records) != 1 || h.host.records[0].Kind != "todou.delivery_failed" {
		t.Fatalf("records = %+v", h.host.records)
	}
	h.fail = nil
	_ = h.poll(t)
	if len(h.sends) != 1 {
		t.Fatal("a given-up notification was sent again")
	}
	if status := h.command(t, "chat:a", "", otogi.DirectionOutgoing, "status"); !strings.Contains(status, "1 failed") || !strings.Contains(status, "CHAT_WRITE_FORBIDDEN") {
		t.Fatalf("status = %q", status)
	}
}

func TestRetryUsesTheSameOrigin(t *testing.T) {
	h := newHarness(t, Config{})
	h.command(t, "chat:a", "", otogi.DirectionOutgoing, "link web")
	h.sends = nil
	_ = h.poll(t)
	attempts := 0
	h.fail = func(otogi.ConversationID) error {
		attempts++
		if attempts == 1 {
			return errors.New("timeout")
		}
		return nil
	}
	h.cli.watchOutputs = []string{lines(comment(1, "web", 1, "x"), cursor("c1")), lines(cursor("c2"))}
	_ = h.poll(t)
	_ = h.poll(t)
	if len(h.sends) != 1 || h.sends[0].origin != "outbox:1" {
		t.Fatalf("sends = %+v", h.sends)
	}
}

func TestPollFailureKeepsTheCursorAndShowsInStatus(t *testing.T) {
	h := newHarness(t, Config{})
	_ = h.poll(t)
	h.cli.watchErr = &cliError{args: []string{"watch"}, code: 4, stderr: "gave up on a network outage"}
	if err := h.poll(t); err == nil {
		t.Fatal("poll succeeded")
	}
	h.cli.watchErr = nil
	if status := h.command(t, "chat:a", "", otogi.DirectionOutgoing, "status"); !strings.Contains(status, "exit 4") || !strings.Contains(status, "Notifier (@bot, machine)") {
		t.Fatalf("status = %q", status)
	}
	h.cli.watchOutputs = []string{lines(cursor("c1"))}
	_ = h.poll(t)
	if got := h.cli.sinces(); !slices.Equal(got, []string{"c0", "c0"}) {
		t.Fatalf("cursors = %v; a failed poll must not move the cursor", got)
	}
}

func TestCommands(t *testing.T) {
	h := newHarness(t, Config{})
	steps := []struct {
		actor     otogi.ActorID
		direction otogi.Direction
		args      string
		want      string
	}{
		{"user:9", otogi.DirectionIncoming, "link web", "Your ID is telegram:user:9"},
		{"user:1", otogi.DirectionIncoming, "link nope", "cannot read a project named \"nope\""},
		{"user:1", otogi.DirectionIncoming, "link", "Usage: /todou link"},
		{"user:1", otogi.DirectionIncoming, "link web", "Linked web"},
		{"", otogi.DirectionOutgoing, "link web", "already linked"},
		{"user:9", otogi.DirectionIncoming, "list", "Linked todou projects: web"},
		{"user:1", otogi.DirectionIncoming, "unlink web", "Unlinked web"},
		{"user:1", otogi.DirectionIncoming, "list", "No todou projects"},
		{"user:9", otogi.DirectionIncoming, "frobnicate", "Usage: /todou"},
	}
	for _, s := range steps {
		if got := h.command(t, "chat:a", s.actor, s.direction, s.args); !strings.Contains(got, s.want) {
			t.Errorf("/todou %s by %q = %q, want %q", s.args, s.actor, got, s.want)
		}
	}
}

func TestRendering(t *testing.T) {
	r := renderer{linkBase: "https://todou.test", maxBody: 10, title: func(p string, n int64) issue { return issue{Title: "T", Body: "opened body is long"} }}
	hidden := "2026-10-16T00:00:00Z"
	entries := []entry{
		{Type: "comment", Project: "web", IssueNumber: 1, IssueRef: "web#1", Author: &user{DisplayName: "Alice"}, Body: "a long comment body", AgentContext: &struct {
			Agent string `json:"agent"`
		}{Agent: "claude-code"}},
		{Type: "comment", Project: "web", IssueNumber: 1, IssueRef: "web#1", Author: &user{Login: "bob"}, HiddenAt: &hidden},
		{Type: "event", EventType: "status_changed", Project: "web", IssueNumber: 1, IssueRef: "web#1", Actor: &user{Login: "bob"},
			Payload: map[string]any{"from": map[string]any{"name": "Todo"}, "to": map[string]any{"name": "Doing"}}},
		{Type: "event", EventType: "opened", Project: "web", IssueNumber: 2, Actor: &user{Login: "c"}},
		{Type: "event", EventType: "brand_new_kind", Project: "web", IssueNumber: 2, Actor: &user{Login: "c"}},
	}
	msgs := r.messages(entries)
	if len(msgs) != 1 {
		t.Fatalf("messages = %d", len(msgs))
	}
	text := msgs[0].PlainText()
	for _, want := range []string{"💬 web#1 “T” — Alice (claude-code) commented:\n  a long com…", "bob commented (hidden comment)", "moved it Todo → Doing", "🆕 web#2 “T” — c opened it:\n  opened bod…", "brand new kind"} {
		if !strings.Contains(text, want) {
			t.Errorf("text lacks %q:\n%s", want, text)
		}
	}
	if err := msgs[0].Validate(); err != nil {
		t.Fatal(err)
	}
	if link := msgs[0].Parts[0].Annotations[0]; link.Kind != otogi.AnnotationLink || link.Target != "https://todou.test/projects/web/issues/1" {
		t.Fatalf("first annotation = %+v", link)
	}

	many := make([]entry, 60)
	for i := range many {
		many[i] = entry{Type: "comment", Project: "web", IssueNumber: int64(i), Author: &user{Login: "a"}, Body: strings.Repeat("汉字", 150)}
	}
	r.maxBody = 300
	split := r.messages(many)
	for _, m := range split {
		if n := utf16Len(m.PlainText()); n > messageLimit {
			t.Fatalf("message of %d UTF-16 units exceeds the limit", n)
		}
		if err := m.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	if len(split) < 2 || !strings.HasPrefix(split[1].PlainText(), "💬") {
		t.Fatalf("split into %d messages", len(split))
	}
}

// The real subprocess path: credentials in the environment, NDJSON on
// stdout, exit codes and stderr surfaced.
func TestExecRunner(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "todou")
	body := "#!/bin/sh\n" +
		`if [ "$TODOU_SERVER" != "https://todou.test" ] || [ "$TODOU_TOKEN" != "todou_pat_x" ]; then echo "bad env" >&2; exit 1; fi` + "\n" +
		`if [ "$1" = fail ]; then echo "gave up on a network outage" >&2; exit 4; fi` + "\n" +
		`echo '{"type":"comment","id":1,"project":"web","issue_number":1,"body":"hi"}'` + "\n" +
		`echo '{"type":"cursor","next_cursor":"c9"}'` + "\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	c := client{run: execRunner(script, "https://todou.test", "todou_pat_x", 5*time.Second)}
	entries, next, err := c.watch(context.Background(), "c0", nil)
	if err != nil || len(entries) != 1 || entries[0].Body != "hi" || next != "c9" {
		t.Fatalf("watch = %+v, %q, %v", entries, next, err)
	}
	_, err = c.run(context.Background(), "fail")
	var cliErr *cliError
	if !errors.As(err, &cliErr) || cliErr.code != 4 || !strings.Contains(cliErr.stderr, "network outage") {
		t.Fatalf("error = %v", err)
	}
}

func TestAFailedRowHoldsBackLaterRowsOfItsChat(t *testing.T) {
	h := newHarness(t, Config{})
	h.command(t, "chat:a", "", otogi.DirectionOutgoing, "link web")
	h.sends = nil
	_ = h.poll(t)
	calls := 0
	h.fail = func(otogi.ConversationID) error {
		calls++
		if calls <= 3 { // "one" fails on every attempt of the first two polls
			return errors.New("timeout")
		}
		return nil
	}
	h.cli.watchOutputs = []string{lines(comment(1, "web", 1, "one"), cursor("c1")), lines(comment(2, "web", 1, "two"), cursor("c2")), lines(cursor("c3"))}
	_ = h.poll(t)
	_ = h.poll(t) // "two" is now queued behind the still-failing "one"
	if len(h.sends) != 0 {
		t.Fatalf("sent %q past a failed row", h.sends[0].msg.PlainText())
	}
	_ = h.poll(t)
	if len(h.sends) != 2 || !strings.Contains(h.sends[0].msg.PlainText(), "one") || !strings.Contains(h.sends[1].msg.PlainText(), "two") {
		t.Fatalf("sends = %+v", h.sends)
	}
}

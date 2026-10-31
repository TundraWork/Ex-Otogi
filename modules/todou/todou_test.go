package todou

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pressly/goose/v3"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/modulestore"
	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// fakeCLI answers one-shot CLI calls and keeps them, with their stdin.
// Project "web" has prefix WEB and "api" API; cards up to 100 exist.
type fakeCLI struct {
	projects []string
	mu       sync.Mutex
	calls    [][]string
	stdins   []string
}

func (f *fakeCLI) run(_ context.Context, stdin string, args ...string) ([]byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, args)
	f.stdins = append(f.stdins, stdin)
	f.mu.Unlock()
	fail := func(msg string) ([]byte, error) { return nil, &cliError{args: args, code: 1, stderr: msg} }
	verb := args[0]
	if len(args) > 1 {
		verb += " " + args[1]
	}
	switch verb {
	case "watch --all-projects": // --poll --print-cursor
		return []byte("c0\n"), nil
	case "issue view":
		ref, project := args[2], ""
		if i := slices.Index(args, "-p"); i >= 0 {
			project = args[i+1]
		}
		var number int
		switch {
		case strings.Contains(ref, "/"):
			project, number = strings.Split(ref, "/")[0], int(atoi(strings.Split(ref, "/")[1]))
		case strings.Contains(ref, "-"):
			prefix, n, _ := strings.Cut(ref, "-")
			if strings.ToLower(prefix) != project {
				return fail("prefix " + prefix + " contradicts -p " + project)
			}
			number = int(atoi(n))
		}
		if number < 1 || number > 100 {
			return fail("no such issue")
		}
		return []byte(fmt.Sprintf(`{"issue":{"number":%d,"ref":"%s-%d","title":"Title of %s/%d","body":"Body of %s/%d","status":{"name":"Todo","category":"open"},
			"assignees":[{"login":"alice"}],"labels":[{"name":"kind:bug"}],"blocked_by":[{"ref":"%s-3","cleared_at":null},{"ref":"%s-4","cleared_at":"2026-10-01T00:00:00Z"}],"blocks":[]},"ref_format":{}}`,
			number, strings.ToUpper(project), number, project, number, project, number, strings.ToUpper(project), strings.ToUpper(project))), nil
	case "issue create":
		return []byte(`{"number":31,"ref":"WEB-31","title":"x"}`), nil
	case "comment add":
		return []byte(`{"id":501,"cursor":"c9"}`), nil
	case "comment view":
		return []byte(`{"type":"comment","component":{"type":"questions","questions":[{"key":"q1","header":"Scope","question":"Which?"}]}}`), nil
	case "comment list":
		return []byte(`{"comments":[{"id":7,"kind":"comment","author":{"login":"alice"},"body":"Looks broken","created_at":"2026-10-30T00:00:00Z"}]}`), nil
	case "issue status", "issue edit", "issue block", "issue unblock":
		return []byte(`{}`), nil
	case "label list":
		return []byte(`[{"name":"kind:bug"},{"name":"area:ui"}]`), nil
	case "status list":
		return []byte(`[{"name":"Todo","category":"open"},{"name":"Done","category":"closed"}]`), nil
	case "project members":
		return []byte(`[{"user":{"login":"alice"}},{"user":{"login":"bob"}}]`), nil
	case "search " + args[1]:
		return []byte(`{"items":[{"kind":"issue","field":"title","issue":{"ref":"WEB-5","title":"Login crash","status":{"name":"Todo"}},"snippet":{"text":"Login crash"}}]}`), nil
	case "project list":
		var ps []map[string]any
		for i, p := range f.projects {
			ps = append(ps, map[string]any{"id": i + 1, "slug": p})
		}
		return json.Marshal(ps)
	case "whoami --json":
		return []byte(`{"login":"bot","display_name":"Notifier","kind":"machine"}`), nil
	}
	return nil, fmt.Errorf("unexpected call %v", args)
}

// last returns the most recent call starting with prefix, and its stdin.
func (f *fakeCLI) last(prefix ...string) (string, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.calls) - 1; i >= 0; i-- {
		if len(f.calls[i]) >= len(prefix) && slices.Equal(f.calls[i][:len(prefix)], prefix) {
			return strings.Join(f.calls[i], " "), f.stdins[i]
		}
	}
	return "", ""
}

// idle is a watcher that sees no activity.
func idle(ctx context.Context, _ []string, _ func([]byte) error, _ func(string)) error {
	<-ctx.Done()
	return ctx.Err()
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

// fakeHost keeps records and timers; the watcher goroutine uses it too.
type fakeHost struct {
	otogi.Host
	storage  *modulestore.Storage
	mu       sync.Mutex
	records  []otogi.Record
	timers   map[string]otogi.Timer
	provided map[string]any
}

func (h *fakeHost) Logger() *slog.Logger { return slog.New(slog.DiscardHandler) }
func (h *fakeHost) Provide(name string, service any) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.provided == nil {
		h.provided = map[string]any{}
	}
	h.provided[name] = service
	return nil
}
func (h *fakeHost) Database(ctx context.Context) (*sql.DB, error) {
	return h.storage.Database(ctx, "todou")
}
func (h *fakeHost) Record(_ context.Context, r otogi.Record) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r)
}
func (h *fakeHost) SetTimer(_ context.Context, key string, at time.Time, payload []byte) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.timers[key] = otogi.Timer{Key: key, At: at, Payload: payload}
	return nil
}
func (h *fakeHost) kinds() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var kinds []string
	for _, r := range h.records {
		kinds = append(kinds, r.Kind)
	}
	return kinds
}

// clock is the module's time in tests.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type harness struct {
	m       *Module
	host    *fakeHost
	clock   *clock
	storage *modulestore.Storage
	cli     *fakeCLI
	sends   []sent
	fail    func(otogi.ConversationID) error
}

func newHarness(t *testing.T, cfg Config, stream streamer) *harness {
	t.Helper()
	storage, err := modulestore.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	return startHarness(t, storage, cfg, stream)
}

// startHarness starts the module on storage and waits for its watcher to
// run. The module is stopped when the test ends.
func startHarness(t *testing.T, storage *modulestore.Storage, cfg Config, stream streamer) *harness {
	t.Helper()
	cfg.CLI = "todou"
	if cfg.Agents == nil {
		cfg.Agents = map[string]AgentConfig{"neptune": {Server: "https://todou.test", Token: "todou_pat_x"}}
	}
	m, err := New(cfg, []string{"telegram:user:1"})
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{m: m, host: &fakeHost{storage: storage, timers: map[string]otogi.Timer{}}, clock: &clock{t: time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)}, storage: storage}
	m.now = h.clock.now
	h.cli = &fakeCLI{projects: []string{"web", "api"}}
	for _, a := range m.agents {
		a.cli = client{run: h.cli.run, stream: stream}
	}
	if err := m.Start(context.Background(), h.host); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := m.Stop(context.Background()); err != nil {
			t.Error(err)
		}
	})
	for _, a := range m.agents {
		waitFor(t, "the watcher to run", func() bool {
			a.mu.Lock()
			defer a.mu.Unlock()
			return !a.watcher.since.IsZero() || a.watcher.err != ""
		})
	}
	return h
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !cond(); {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func chat(id otogi.ConversationID) otogi.Destination {
	return otogi.Destination{Platform: "telegram", Account: "main", Conversation: id}
}

func (h *harness) inv() *fakeInvocation {
	return &fakeInvocation{host: h.host, origin: "timer", sends: &h.sends, fail: h.fail}
}

// timer returns a chat's digest timer, if armed.
func (h *harness) timer(conversation otogi.ConversationID) (otogi.Timer, bool) {
	payload, _ := json.Marshal(chat(conversation))
	h.host.mu.Lock()
	defer h.host.mu.Unlock()
	t, ok := h.host.timers["digest:"+string(payload)]
	return t, ok
}

// fire runs a chat's digest timer and, like the kernel, deletes it after a
// successful run unless the run re-armed it.
func (h *harness) fire(t *testing.T, conversation otogi.ConversationID) error {
	t.Helper()
	timer, ok := h.timer(conversation)
	if !ok {
		t.Fatalf("no digest timer for %s", conversation)
	}
	err := h.m.HandleTimer(context.Background(), timer, h.inv())
	h.host.mu.Lock()
	defer h.host.mu.Unlock()
	if err == nil && h.host.timers[timer.Key].At.Equal(timer.At) {
		delete(h.host.timers, timer.Key)
	}
	return err
}

// batch hands the module a watch batch of records, as the watcher would.
func (h *harness) batch(t *testing.T, next string, records ...string) {
	t.Helper()
	var entries []entry
	for _, r := range records {
		e, err := decodeEntry(r)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, e)
	}
	if err := h.m.ingest(context.Background(), h.m.agents["neptune"], entries, next); err != nil {
		t.Fatal(err)
	}
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

func TestWatcherQueuesLinkedActivityAndRestartsOnFailure(t *testing.T) {
	release := make(chan struct{})
	var mu sync.Mutex
	var calls [][]string
	stream := func(ctx context.Context, args []string, stdout func([]byte) error, stderr func(string)) error {
		mu.Lock()
		calls = append(calls, args)
		first := len(calls) == 1
		mu.Unlock()
		if !first {
			return idle(ctx, args, stdout, stderr)
		}
		<-release
		for _, line := range []string{comment(1, "web", 42, "linked"), comment(2, "other", 1, "unlinked"), cursor("c1"), comment(3, "web", 42, "no cursor yet: replayed")} {
			if err := stdout([]byte(line)); err != nil {
				return err
			}
		}
		return &cliError{args: args, code: 1, stderr: "token revoked"}
	}
	h := newHarness(t, Config{}, stream)
	h.command(t, "chat:a", "", otogi.DirectionOutgoing, "link neptune web")
	close(release)
	waitFor(t, "the watcher failure", func() bool { return slices.Contains(h.host.kinds(), "todou.watch_failed") })

	mu.Lock()
	args := strings.Join(calls[0], " ")
	mu.Unlock()
	if !strings.Contains(args, "--follow --invisible --json --any-actor --since c0 --debounce 10 --type comment,opened") {
		t.Errorf("watch args = %q", args)
	}
	if got, _ := h.m.store.cursor(context.Background(), "neptune"); got != "c1" {
		t.Errorf("cursor = %q, want c1", got)
	}
	queue, err := h.m.store.queue(context.Background(), chat("chat:a"))
	if err != nil || len(queue) != 1 || queue[0].Entry.Body != "linked" {
		t.Fatalf("queue = %+v, %v", queue, err)
	}
	if _, ok := h.timer("chat:a"); !ok {
		t.Fatal("digest timer not armed")
	}
	if status := h.command(t, "chat:a", "", otogi.DirectionOutgoing, "status"); !strings.Contains(status, "Watcher: failed 0s ago, restarting: todou watch") || !strings.Contains(status, "token revoked") ||
		!strings.Contains(status, "Queued here: 1 entries") || !strings.Contains(status, "Notifier (@bot, machine)") {
		t.Fatalf("status = %q", status)
	}
}

func TestDigestsAreThrottledAndAggregated(t *testing.T) {
	h := newHarness(t, Config{}, idle)
	h.command(t, "chat:a", "", otogi.DirectionOutgoing, "link neptune web")
	h.sends = nil

	h.batch(t, "c1", comment(1, "web", 42, "first"))
	if err := h.fire(t, "chat:a"); err != nil {
		t.Fatal(err)
	}
	if len(h.sends) != 1 || !strings.Contains(h.sends[0].msg.PlainText(), "first") {
		t.Fatalf("first digest = %+v", h.sends)
	}

	h.clock.advance(time.Minute)
	h.batch(t, "c2", comment(2, "web", 7, "second"))
	h.batch(t, "c3", comment(3, "web", 42, "third"))
	if err := h.fire(t, "chat:a"); err != nil {
		t.Fatal(err)
	}
	if timer, ok := h.timer("chat:a"); len(h.sends) != 1 || !ok || !timer.At.Equal(h.clock.now().Add(4*time.Minute)) {
		t.Fatalf("within min_interval: sends = %d, timer = %+v", len(h.sends), timer)
	}

	h.clock.advance(4 * time.Minute)
	if err := h.fire(t, "chat:a"); err != nil {
		t.Fatal(err)
	}
	if len(h.sends) != 2 {
		t.Fatalf("sends = %d, want one aggregated digest", len(h.sends))
	}
	text := h.sends[1].msg.PlainText()
	if !strings.HasPrefix(text, "web#7 Title of web/7\n💬 Alice commented\nsecond\n\nweb#42 Title of web/42\n💬 Alice commented\nthird") {
		t.Fatalf("digest = %q", text)
	}
	if h.sends[0].origin == h.sends[1].origin || !strings.HasPrefix(h.sends[1].origin, "outbox:") || h.sends[1].slot != "notify" {
		t.Fatalf("digests keyed by %q and %q", h.sends[0].origin, h.sends[1].origin)
	}
	if _, ok := h.timer("chat:a"); ok {
		t.Fatal("timer left armed with nothing queued")
	}
}

func TestFailuresBlockTheChatUntilGivenUp(t *testing.T) {
	h := newHarness(t, Config{MaxAttempts: 2}, idle)
	h.command(t, "chat:a", "", otogi.DirectionOutgoing, "link neptune web")
	h.command(t, "chat:b", "", otogi.DirectionOutgoing, "link neptune web")
	h.sends = nil
	h.fail = func(c otogi.ConversationID) error {
		if c == "chat:a" {
			return errors.New("CHAT_WRITE_FORBIDDEN")
		}
		return nil
	}
	h.batch(t, "c1", comment(1, "web", 1, "x"))
	if err := h.fire(t, "chat:a"); err == nil || !strings.Contains(err.Error(), "attempt 1 of 2") {
		t.Fatalf("first attempt: %v", err)
	}
	if err := h.fire(t, "chat:b"); err != nil || len(h.sends) != 1 {
		t.Fatalf("a failing chat blocked another: %v, %+v", err, h.sends)
	}

	h.batch(t, "c2", comment(2, "web", 1, "y")) // queued behind the failing digest
	// The second attempt gives up on "x" and moves on to a digest of "y",
	// whose first attempt fails.
	if err := h.fire(t, "chat:a"); err == nil || !strings.Contains(err.Error(), "row 3 (attempt 1 of 2)") {
		t.Fatalf("second run: %v", err)
	}
	if kinds := h.host.kinds(); !slices.Equal(kinds, []string{"todou.delivery_failed"}) {
		t.Fatalf("records = %v", kinds)
	}
	h.fail = nil
	if err := h.fire(t, "chat:a"); err != nil {
		t.Fatal(err)
	}
	if len(h.sends) != 2 || !strings.Contains(h.sends[1].msg.PlainText(), "y") {
		t.Fatalf("sends = %+v", h.sends)
	}
	if status := h.command(t, "chat:a", "", otogi.DirectionOutgoing, "status"); !strings.Contains(status, "1 failed") || !strings.Contains(status, "CHAT_WRITE_FORBIDDEN") {
		t.Fatalf("status = %q", status)
	}
}

func TestRetryUsesTheSameOrigin(t *testing.T) {
	h := newHarness(t, Config{}, idle)
	h.command(t, "chat:a", "", otogi.DirectionOutgoing, "link neptune web")
	h.sends = nil
	attempts := 0
	h.fail = func(otogi.ConversationID) error {
		if attempts++; attempts == 1 {
			return errors.New("timeout")
		}
		return nil
	}
	h.batch(t, "c1", comment(1, "web", 1, "x"))
	if err := h.fire(t, "chat:a"); err == nil {
		t.Fatal("failed delivery did not fail the timer run")
	}
	if err := h.fire(t, "chat:a"); err != nil {
		t.Fatal(err)
	}
	if len(h.sends) != 1 || h.sends[0].origin != "outbox:1" {
		t.Fatalf("sends = %+v", h.sends)
	}
}

func TestUnlinkDropsQueuedEntries(t *testing.T) {
	h := newHarness(t, Config{}, idle)
	h.command(t, "chat:a", "", otogi.DirectionOutgoing, "link neptune web")
	h.command(t, "chat:a", "", otogi.DirectionOutgoing, "link neptune api")
	h.batch(t, "c1", comment(1, "web", 1, "web news"), comment(2, "api", 1, "api news"))
	h.command(t, "chat:a", "", otogi.DirectionOutgoing, "unlink web")
	h.sends = nil
	if err := h.fire(t, "chat:a"); err != nil {
		t.Fatal(err)
	}
	if text := h.sends[0].msg.PlainText(); strings.Contains(text, "web news") || !strings.Contains(text, "api news") {
		t.Fatalf("digest = %q", text)
	}
}

func TestStartRearmsChatsWithABacklog(t *testing.T) {
	storage, err := modulestore.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	first := startHarness(t, storage, Config{}, idle)
	first.command(t, "chat:a", "", otogi.DirectionOutgoing, "link neptune web")
	first.batch(t, "c1", comment(1, "web", 1, "x"))
	if err := first.m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}

	second := startHarness(t, storage, Config{}, idle) // its host has no timers
	if _, ok := second.timer("chat:a"); !ok {
		t.Fatal("the backlog's timer was not re-armed")
	}
	if err := second.fire(t, "chat:a"); err != nil || len(second.sends) != 1 {
		t.Fatalf("sends = %+v, %v", second.sends, err)
	}
}

func TestCommands(t *testing.T) {
	h := newHarness(t, Config{}, idle)
	steps := []struct {
		actor     otogi.ActorID
		direction otogi.Direction
		args      string
		want      string
	}{
		{"user:9", otogi.DirectionIncoming, "link neptune web", "Your ID is telegram:user:9"},
		{"user:1", otogi.DirectionIncoming, "link neptune nope", "neptune cannot read a project named \"nope\""},
		{"user:1", otogi.DirectionIncoming, "link akarin web", "There is no agent called \"akarin\". Agents: neptune."},
		{"user:1", otogi.DirectionIncoming, "link web", "Usage: /todou link <agent> <project> [silent]. Agents: neptune"},
		{"user:1", otogi.DirectionIncoming, "link neptune web", "Linked web through neptune. New activity"},
		{"", otogi.DirectionOutgoing, "link neptune web", "web is already linked here, through neptune."},
		{"", otogi.DirectionOutgoing, "link neptune web silent", "Linked web through neptune, silently"},
		{"user:9", otogi.DirectionIncoming, "list", "Linked todou projects:\n• web, through neptune, silently"},
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

func TestCooldownCommand(t *testing.T) {
	h := newHarness(t, Config{}, idle)
	h.command(t, "chat:a", "", otogi.DirectionOutgoing, "link neptune web")
	steps := []struct {
		actor     otogi.ActorID
		direction otogi.Direction
		args      string
		want      string
	}{
		{"user:9", otogi.DirectionIncoming, "cooldown", "Cooldown: 5m (default)."},
		{"user:9", otogi.DirectionIncoming, "cooldown 1m", "Your ID is telegram:user:9"},
		{"user:1", otogi.DirectionIncoming, "cooldown soon", `"soon" is not a duration`},
		{"user:1", otogi.DirectionIncoming, "cooldown -1m", `"-1m" is not a duration`},
		{"user:1", otogi.DirectionIncoming, "cooldown 90m", "Cooldown set to 1h30m."},
		{"user:1", otogi.DirectionIncoming, "cooldown 0", "Cooldown off."},
		{"user:9", otogi.DirectionIncoming, "cooldown", "Cooldown: off."},
	}
	for _, s := range steps {
		if got := h.command(t, "chat:a", s.actor, s.direction, s.args); !strings.Contains(got, s.want) {
			t.Errorf("/todou %s by %q = %q, want %q", s.args, s.actor, got, s.want)
		}
	}

	h.sends = nil
	h.batch(t, "c1", comment(1, "web", 1, "one"))
	if err := h.fire(t, "chat:a"); err != nil {
		t.Fatal(err)
	}
	h.batch(t, "c2", comment(2, "web", 1, "two"))
	if err := h.fire(t, "chat:a"); err != nil {
		t.Fatal(err)
	}
	if len(h.sends) != 2 {
		t.Fatalf("with no cooldown, sends = %d, want each batch at once", len(h.sends))
	}

	h.command(t, "chat:a", "", otogi.DirectionOutgoing, "cooldown 1h")
	h.batch(t, "c3", comment(3, "web", 1, "three"))
	if err := h.fire(t, "chat:a"); err != nil {
		t.Fatal(err)
	}
	if timer, ok := h.timer("chat:a"); !ok || !timer.At.Equal(h.clock.now().Add(time.Hour)) {
		t.Fatalf("timer = %+v, want an hour after the last digest", timer)
	}
	if got := h.command(t, "chat:a", "", otogi.DirectionOutgoing, "cooldown default"); got != "Cooldown reset to the default, 5m." {
		t.Fatalf("reset = %q", got)
	}
	if timer, ok := h.timer("chat:a"); !ok || !timer.At.Equal(h.clock.now()) {
		t.Fatalf("timer = %+v, want re-armed now", timer)
	}
	if status := h.command(t, "chat:a", "", otogi.DirectionOutgoing, "status"); !strings.Contains(status, "\nCooldown: 5m, last digest 0s ago\n") {
		t.Fatalf("status = %q", status)
	}
}

func TestDurationsReadNaturally(t *testing.T) {
	t.Parallel()
	for d, want := range map[time.Duration]string{
		0: "0s", 30 * time.Second: "30s", 10 * time.Minute: "10m", 90 * time.Second: "1m30s",
		time.Hour: "1h", 90 * time.Minute: "1h30m", time.Hour + 5*time.Second: "1h0m5s",
	} {
		if got := duration(d); got != want {
			t.Errorf("duration(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestAgentsAndSilentLinks(t *testing.T) {
	h := newHarness(t, Config{Agents: map[string]AgentConfig{
		"neptune": {Server: "https://todou.test", Token: "a"},
		"akarin":  {Server: "https://other.test", Token: "b", LinkBase: "https://web.other.test/"},
	}}, idle)
	h.command(t, "chat:a", "", otogi.DirectionOutgoing, "link neptune web")
	h.command(t, "chat:b", "", otogi.DirectionOutgoing, "link akarin web")
	h.command(t, "chat:c", "", otogi.DirectionOutgoing, "link neptune web silent")
	ingest := func(agent, next string, records ...string) {
		var entries []entry
		for _, r := range records {
			e, err := decodeEntry(r)
			if err != nil {
				t.Fatal(err)
			}
			entries = append(entries, e)
		}
		if err := h.m.ingest(context.Background(), h.m.agents[agent], entries, next); err != nil {
			t.Fatal(err)
		}
	}
	ingest("neptune", "n1", comment(1, "web", 7, "seen by neptune"))
	ingest("akarin", "a1", comment(2, "web", 7, "seen by akarin"))
	for agent, want := range map[string]string{"neptune": "n1", "akarin": "a1"} {
		if got, _ := h.m.store.cursor(context.Background(), agent); got != want {
			t.Errorf("%s cursor = %q, want %q", agent, got, want)
		}
	}
	for conversation, want := range map[otogi.ConversationID][]string{"chat:a": {"seen by neptune"}, "chat:b": {"seen by akarin"}, "chat:c": nil} {
		queue, err := h.m.store.queue(context.Background(), chat(conversation))
		if err != nil {
			t.Fatal(err)
		}
		var bodies []string
		for _, q := range queue {
			bodies = append(bodies, q.Entry.Body)
		}
		if !slices.Equal(bodies, want) {
			t.Errorf("%s queue = %q, want %q", conversation, bodies, want)
		}
	}
	if err := h.fire(t, "chat:b"); err != nil {
		t.Fatal(err)
	}
	part := h.sends[len(h.sends)-1].msg.Parts[0]
	if !slices.ContainsFunc(part.Annotations, func(a otogi.Annotation) bool { return a.Target == "https://web.other.test/projects/web/issues/7" }) {
		t.Fatalf("akarin's digest does not link through its link_base: %+v", part.Annotations)
	}
	status := h.command(t, "chat:a", "", otogi.DirectionOutgoing, "status")
	if !strings.Contains(status, "Agent akarin: Notifier (@bot, machine) on https://other.test\n  Watcher: running") || !strings.Contains(status, "Agent neptune:") {
		t.Fatalf("status = %q", status)
	}
}

func TestReadsLeaveNoReadMarks(t *testing.T) {
	h := newHarness(t, Config{}, idle)
	h.command(t, "chat:a", "", otogi.DirectionOutgoing, "link neptune web")
	answered := record(map[string]any{"type": "event", "event_type": "question_answered", "id": 9, "project": "web", "issue_number": 3, "issue_ref": "web#3",
		"actor": map[string]any{"login": "john"}, "payload": map[string]any{"comment_id": 55, "answers": []any{map[string]any{"key": "q1", "selected": []any{map[string]any{"label": "This one"}}}}}})
	h.batch(t, "c1", comment(1, "web", 3, "hi"), answered)
	if err := h.fire(t, "chat:a"); err != nil {
		t.Fatal(err)
	}
	if text := h.sends[len(h.sends)-1].msg.PlainText(); !strings.Contains(text, "Scope: Which?\n→ This one") {
		t.Fatalf("the answer lacks its question: %q", text)
	}
	h.cli.mu.Lock()
	defer h.cli.mu.Unlock()
	var views []string
	for _, c := range h.cli.calls {
		if len(c) > 1 && c[0] == "issue" && c[1] == "view" || c[0] == "comment" {
			views = append(views, strings.Join(c, " "))
		}
	}
	if !slices.Equal(views, []string{"issue view web/3 --brief --json", "comment view web/3 55 --json"}) {
		t.Fatalf("reads = %q", views)
	}
}

func TestLinksFromBeforeAgents(t *testing.T) {
	for name, agents := range map[string]map[string]AgentConfig{
		"one agent adopts them":      {"neptune": {Server: "https://todou.test", Token: "a"}},
		"several agents refuse them": {"neptune": {Server: "https://todou.test", Token: "a"}, "akarin": {Server: "https://o.test", Token: "b"}},
	} {
		t.Run(name, func(t *testing.T) {
			storage, err := modulestore.Open(context.Background(), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = storage.Close() })
			db, err := storage.Database(context.Background(), "todou")
			if err != nil {
				t.Fatal(err)
			}
			sub, _ := fs.Sub(migrations, "migrations")
			provider, err := goose.NewProvider(goose.DialectSQLite3, db, sub)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := provider.UpTo(context.Background(), 3); err != nil {
				t.Fatal(err)
			}
			for _, q := range []string{
				"INSERT INTO state (singleton, cursor, batch_at) VALUES (1, 'old-cursor', 1)",
				"INSERT INTO links (platform, account, conversation, project, created_by, created_at) VALUES ('telegram', 'main', 'chat:a', 'web', 'x', 1)",
			} {
				if _, err := db.Exec(q); err != nil {
					t.Fatal(err)
				}
			}
			m, err := New(Config{CLI: "todou", Agents: agents}, nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, a := range m.agents {
				a.cli = client{run: (&fakeCLI{}).run, stream: idle}
			}
			host := &fakeHost{storage: storage, timers: map[string]otogi.Timer{}}
			err = m.Start(context.Background(), host)
			if len(agents) > 1 {
				if err == nil || !strings.Contains(err.Error(), "name no agent") {
					t.Fatalf("Start = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = m.Stop(context.Background()) })
			links, err := m.store.links(context.Background(), nil)
			if err != nil || len(links) != 1 || links[0].Agent != "neptune" || links[0].Silent {
				t.Fatalf("links = %+v, %v", links, err)
			}
			if got, _ := m.store.cursor(context.Background(), "neptune"); got != "old-cursor" {
				t.Fatalf("cursor = %q", got)
			}
		})
	}
}

// say sends a /todou message as Bob in the group "Friends" and returns the
// reply; text is the whole message, rich lets it carry formatting.
func (h *harness) say(t *testing.T, rich *otogi.RichText) string {
	t.Helper()
	text := rich.String()
	args := strings.TrimSpace(strings.TrimPrefix(text, "/todou"))
	e := otogi.Event{ID: "e1", Platform: "telegram", Account: "main", Direction: otogi.DirectionIncoming,
		Conversation: otogi.Conversation{ID: "chat:a", Kind: otogi.ConversationGroup, Title: "Friends"},
		Actor:        otogi.Actor{ID: "user:9", Name: "Bob"}, Message: rich.Message(), Command: &otogi.Command{Name: "todou", Args: args}}
	before := len(h.sends)
	if err := h.m.Handle(context.Background(), e, h.inv()); err != nil {
		t.Fatal(err)
	}
	return h.sends[before].msg.PlainText()
}

func plain(text string) *otogi.RichText {
	r := &otogi.RichText{}
	r.Write(text)
	return r
}

func TestWriteCommands(t *testing.T) {
	h := newHarness(t, Config{}, idle)
	h.command(t, "chat:a", "", otogi.DirectionOutgoing, "link neptune web silent")
	const line = "— Bob, in Telegram “Friends”"

	filing := plain("/todou new web Crash on start\nIt ")
	filing.Mark(otogi.AnnotationBold, "", func() { filing.Write("crashes") })
	filing.Write(" when I open it.\nEvery time.")
	if got := h.say(t, filing); got != "Filed WEB-31: Crash on start\nhttps://todou.test/projects/web/issues/31" {
		t.Fatalf("new: %q", got)
	}
	call, stdin := h.cli.last("issue", "create")
	if call != "issue create -p web --title Crash on start --body-file - --json" || stdin != "It **crashes** when I open it.\nEvery time.\n\n"+line {
		t.Fatalf("create: %q stdin %q", call, stdin)
	}

	for _, c := range []struct{ say, want, call, stdin string }{
		{"/todou comment WEB-12 please check\nthe logs", "Commented on WEB-12.\nhttps://todou.test/projects/web/issues/12#comment-501", "comment add web/12 --body-file - --json", "please check\nthe logs\n\n" + line},
		{"/todou comment 12 bare number works", "Commented on WEB-12.", "comment add web/12", "bare number works\n\n" + line},
		{"/todou move web/12 In Progress", "Moved WEB-12 to In Progress.", "issue status web/12 In Progress --json", ""},
		{"/todou assign WEB-12 @alice bob", "Assigned @alice, @bob to WEB-12.", "issue edit web/12 --json --add-assignee alice --add-assignee bob", ""},
		{"/todou unassign WEB-12 alice", "Unassigned @alice from WEB-12.", "issue edit web/12 --json --remove-assignee alice", ""},
		{"/todou block WEB-12 by WEB-3, web/4", "WEB-12 is now blocked by WEB-3, WEB-4.", "issue block web/12 --json --by web/3,web/4", ""},
		{"/todou unblock WEB-12 blocks WEB-20", "WEB-12 no longer blocks WEB-20.", "issue unblock web/12 --json --blocks web/20", ""},
		{"/todou view https://todou.test/projects/web/issues/12", "WEB-12 Title of web/12\nStatus: Todo · @alice · kind:bug\nBlocked by: WEB-3\nhttps://todou.test/projects/web/issues/12", "", ""},
		{"/todou comment api/1 hi", "api is not linked to this chat.", "", ""},
		{"/todou new api Some title", "api is not linked to this chat.", "", ""},
		{"/todou move WEB-999 Done", "No card WEB-999 in the projects linked to this chat.", "", ""},
		{"/todou block WEB-12 sideways WEB-3", "Usage: /todou block <card> by|blocks <card>…", "", ""},
	} {
		got := h.say(t, plain(c.say))
		if !strings.HasPrefix(got, c.want) {
			t.Errorf("%s:\n got %q\nwant %q", c.say, got, c.want)
			continue
		}
		if c.call == "" {
			continue
		}
		fields := strings.Fields(c.call)
		call, stdin := h.cli.last(fields[0], fields[1], fields[2])
		if !strings.HasPrefix(call, c.call) || stdin != c.stdin {
			t.Errorf("%s: call %q stdin %q, want %q stdin %q", c.say, call, stdin, c.call, c.stdin)
		}
	}
	var writes []string
	for _, r := range h.host.records {
		if r.Kind == "todou.write" {
			p := r.Payload.(map[string]any)
			writes = append(writes, fmt.Sprintf("%s %s by %s via %s", p["op"], p["project"], p["by"], p["path"]))
		}
	}
	if len(writes) != 8 || writes[0] != "create web by telegram:user:9 via command" {
		t.Fatalf("writes = %q", writes)
	}
}

func TestTools(t *testing.T) {
	h := newHarness(t, Config{}, idle)
	h.command(t, "chat:a", "", otogi.DirectionOutgoing, "link neptune web")
	tools := map[string]llm.Tool{}
	for _, tool := range h.host.provided[llm.ToolsService("todou")].([]llm.Tool) {
		tools[tool.Function().Name] = tool
	}
	e := otogi.Event{ID: "e2", Platform: "telegram", Account: "main", Conversation: otogi.Conversation{ID: "chat:a", Kind: otogi.ConversationDirect}, Actor: otogi.Actor{ID: "user:9", Name: "Bob"}}
	ctx := llm.ContextWithAgent(context.Background(), "Luna")
	call := func(name, args string) (string, error) {
		out, err := tools[name].Call(ctx, e, json.RawMessage(args))
		b, _ := json.Marshal(out)
		return string(b), err
	}
	if _, err := call("todou_create", `{"project":"web","title":"Crash","body":"It crashes.","labels":["kind:typo"]}`); err == nil || !strings.Contains(err.Error(), `no label "kind:typo"; its labels are: kind:bug, area:ui`) {
		t.Fatalf("unknown label: %v", err)
	}
	if out, err := call("todou_create", `{"project":"web","title":"Crash","body":"It crashes.","labels":["kind:bug"]}`); err != nil || out != `{"ref":"WEB-31","url":"https://todou.test/projects/web/issues/31"}` {
		t.Fatalf("create: %s %v", out, err)
	}
	if call, stdin := h.cli.last("issue", "create"); !strings.Contains(call, "--label kind:bug") || stdin != "It crashes.\n\n— Bob, in a private Telegram chat via Luna" {
		t.Fatalf("create call %q stdin %q", call, stdin)
	}
	if out, err := call("todou_view", `{"card":"WEB-12"}`); err != nil || !strings.Contains(out, `"body":"Body of web/12"`) || !strings.Contains(out, `"body":"Looks broken"`) {
		t.Fatalf("view: %s %v", out, err)
	}
	if out, err := call("todou_search", `{"terms":"login crash"}`); err != nil || !strings.Contains(out, `"card":"WEB-5"`) {
		t.Fatalf("search: %s %v", out, err)
	}
	if call, _ := h.cli.last("search"); call != "search login crash -p web -L 10 --json" {
		t.Fatalf("search call %q", call)
	}
	if _, err := call("todou_block", `{"card":"WEB-12","blocked_by":["api/2"]}`); err == nil || !strings.HasPrefix(err.Error(), "api is not linked to this chat. An operator can link it with /todou link <agent> api") {
		t.Fatalf("block across an unlinked project: %v", err)
	}
	if out, err := call("todou_projects", `{}`); err != nil || !strings.Contains(out, `"closed_statuses":["Done"]`) || !strings.Contains(out, `"members":["alice","bob"]`) {
		t.Fatalf("projects: %s %v", out, err)
	}
}

func TestToolsSayWhenNothingIsLinked(t *testing.T) {
	h := newHarness(t, Config{}, idle)
	e := otogi.Event{Platform: "telegram", Account: "main", Conversation: otogi.Conversation{ID: "chat:unlinked"}}
	for _, tool := range h.host.provided[llm.ToolsService("todou")].([]llm.Tool) {
		name := tool.Function().Name
		args := `{"card":"MJT-1","terms":"x","project":"mojito","title":"t","body":"b","status":"Done"}`
		if _, err := tool.Call(context.Background(), e, json.RawMessage(args)); name != "todou_create" && (err == nil || !strings.HasPrefix(err.Error(), "No todou projects are linked to this chat. An operator can link one")) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

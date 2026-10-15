package todou

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/modulestore"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// fakeCLI answers one-shot CLI calls.
type fakeCLI struct{ projects []string }

func (f *fakeCLI) run(_ context.Context, args ...string) ([]byte, error) {
	switch args[0] {
	case "watch": // --poll --print-cursor
		return []byte("c0\n"), nil
	case "issue":
		return []byte(fmt.Sprintf(`{"issue":{"title":"Title of %s","body":"Body of %s"},"timeline":[]}`, args[2], args[2])), nil
	case "project":
		var ps []map[string]string
		for _, p := range f.projects {
			ps = append(ps, map[string]string{"slug": p})
		}
		return json.Marshal(ps)
	case "whoami":
		return []byte(`{"login":"bot","display_name":"Notifier","kind":"machine"}`), nil
	}
	return nil, fmt.Errorf("unexpected call %v", args)
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
	storage *modulestore.Storage
	mu      sync.Mutex
	records []otogi.Record
	timers  map[string]otogi.Timer
}

func (h *fakeHost) Logger() *slog.Logger { return slog.New(slog.DiscardHandler) }
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
	cfg.CLI, cfg.Server, cfg.Token = "todou", "https://todou.test", "todou_pat_x"
	m, err := New(cfg, []string{"telegram:user:1"})
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{m: m, host: &fakeHost{storage: storage, timers: map[string]otogi.Timer{}}, clock: &clock{t: time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)}, storage: storage}
	m.now = h.clock.now
	m.cli = client{run: (&fakeCLI{projects: []string{"web", "api"}}).run, stream: stream}
	if err := m.Start(context.Background(), h.host); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := m.Stop(context.Background()); err != nil {
			t.Error(err)
		}
	})
	waitFor(t, "the watcher to run", func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return !m.watcher.since.IsZero() || m.watcher.err != ""
	})
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
	if err := h.m.ingest(context.Background(), entries, next); err != nil {
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
	h.command(t, "chat:a", "", otogi.DirectionOutgoing, "link web")
	close(release)
	waitFor(t, "the watcher failure", func() bool { return slices.Contains(h.host.kinds(), "todou.watch_failed") })

	mu.Lock()
	args := strings.Join(calls[0], " ")
	mu.Unlock()
	if !strings.Contains(args, "--follow --json --any-actor --since c0 --debounce 10 --type comment,opened") {
		t.Errorf("watch args = %q", args)
	}
	if got, _ := h.m.store.cursor(context.Background()); got != "c1" {
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
	h.command(t, "chat:a", "", otogi.DirectionOutgoing, "link web")
	h.sends = nil

	h.batch(t, "c1", comment(1, "web", 42, "first"))
	if err := h.fire(t, "chat:a"); err != nil {
		t.Fatal(err)
	}
	if len(h.sends) != 1 || strings.Contains(h.sends[0].msg.PlainText(), "updates") || !strings.Contains(h.sends[0].msg.PlainText(), "first") {
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
	if !strings.HasPrefix(text, "🔔 2 todou updates\n\nweb#7 Title of web/7\n💬 Alice commented\nsecond\n\nweb#42 Title of web/42\n💬 Alice commented\nthird") {
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
	h.command(t, "chat:a", "", otogi.DirectionOutgoing, "link web")
	h.command(t, "chat:b", "", otogi.DirectionOutgoing, "link web")
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
	h.command(t, "chat:a", "", otogi.DirectionOutgoing, "link web")
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
	h.command(t, "chat:a", "", otogi.DirectionOutgoing, "link web")
	h.command(t, "chat:a", "", otogi.DirectionOutgoing, "link api")
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
	first.command(t, "chat:a", "", otogi.DirectionOutgoing, "link web")
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

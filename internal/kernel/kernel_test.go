package kernel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

type moduleFunc struct {
	descriptor otogi.ModuleDescriptor
	handle     func(context.Context, otogi.Event, otogi.Invocation) error
}

func (m moduleFunc) Descriptor() otogi.ModuleDescriptor { return m.descriptor }
func (m moduleFunc) Handle(ctx context.Context, e otogi.Event, inv otogi.Invocation) error {
	return m.handle(ctx, e, inv)
}

func module(id otogi.ModuleID, handle func(context.Context, otogi.Event, otogi.Invocation) error) moduleFunc {
	return moduleFunc{descriptor: otogi.ModuleDescriptor{ID: id, Subscribes: []otogi.EventAction{otogi.ActionMessageCreated}}, handle: handle}
}

type middlewareFunc func(context.Context, *otogi.Event) (otogi.Verdict, error)

func (f middlewareFunc) ID() otogi.MiddlewareID { return "mw" }
func (f middlewareFunc) Intercept(ctx context.Context, e *otogi.Event) (otogi.Verdict, error) {
	return f(ctx, e)
}

type messenger struct {
	mu   sync.Mutex
	sent []otogi.OutboundMessage
}

func (m *messenger) Send(_ context.Context, msg otogi.OutboundMessage) (otogi.EventRef, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, msg)
	return otogi.EventRef{Platform: msg.Platform, Account: msg.Account, Conversation: msg.Conversation, SourceEvent: "sent"}, nil
}

func event(conversation string, text string) otogi.Event {
	return otogi.Event{Platform: "test", Account: "bot", Conversation: otogi.Conversation{ID: otogi.ConversationID(conversation)}, Action: otogi.ActionMessageCreated, Message: otogi.TextMessage(text)}
}

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func start(t *testing.T, cfg Config, modules []otogi.Module, middleware []otogi.Middleware, m otogi.Messenger) *Kernel {
	t.Helper()
	if m == nil {
		m = &messenger{}
	}
	k, err := New(cfg, modules, middleware, m, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if err := k.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := k.Stop(ctx); err != nil {
			t.Errorf("Stop: %v", err)
		}
	})
	return k
}

// submit submits e and returns a channel that receives its outcome.
func submit(t *testing.T, k *Kernel, e otogi.Event) <-chan otogi.Outcome {
	t.Helper()
	outcomes := make(chan otogi.Outcome, 1)
	if err := k.Submit(context.Background(), e, func(o otogi.Outcome) { outcomes <- o }); err != nil {
		t.Fatal(err)
	}
	return outcomes
}

func await(t *testing.T, outcomes <-chan otogi.Outcome) otogi.Outcome {
	t.Helper()
	select {
	case o := <-outcomes:
		return o
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for outcome")
		return otogi.Outcome{}
	}
}

func TestNewRejectsInvalidModuleSets(t *testing.T) {
	ok := func(context.Context, otogi.Event, otogi.Invocation) error { return nil }
	tests := map[string][]otogi.Module{
		"empty ID":       {module("", ok)},
		"duplicate ID":   {module("a", ok), module("a", ok)},
		"invalid action": {moduleFunc{descriptor: otogi.ModuleDescriptor{ID: "a", Subscribes: []otogi.EventAction{"relative"}}, handle: ok}},
	}
	for name, modules := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := New(Config{}, modules, nil, &messenger{}, quiet); !errors.Is(err, otogi.ErrInvalid) {
				t.Fatalf("New() = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestFanOutToSubscribedModules(t *testing.T) {
	var mu sync.Mutex
	var seen []otogi.ModuleID
	record := func(id otogi.ModuleID) func(context.Context, otogi.Event, otogi.Invocation) error {
		return func(context.Context, otogi.Event, otogi.Invocation) error {
			mu.Lock()
			seen = append(seen, id)
			mu.Unlock()
			return nil
		}
	}
	edits := moduleFunc{descriptor: otogi.ModuleDescriptor{ID: "edits", Subscribes: []otogi.EventAction{otogi.ActionMessageEdited}}, handle: record("edits")}
	k := start(t, Config{}, []otogi.Module{module("b", record("b")), module("a", record("a")), edits}, nil, nil)

	outcome := await(t, submit(t, k, event("c1", "hi")))
	if outcome.Err != nil || len(outcome.Modules) != 2 || outcome.Modules[0].Module != "a" || outcome.Modules[1].Module != "b" {
		t.Fatalf("outcome = %+v", outcome)
	}
	if outcome.EventID == "" {
		t.Fatal("kernel did not assign an event ID")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 {
		t.Fatalf("modules run = %v, want a and b only", seen)
	}

	unsubscribed := event("c1", "hi")
	unsubscribed.Action = "https://example.org/other"
	if o := await(t, submit(t, k, unsubscribed)); o.Err != nil || len(o.Modules) != 0 {
		t.Fatalf("event with no subscribers: %+v", o)
	}
}

func TestSubmitRejectsInvalidEventAndUnstartedKernel(t *testing.T) {
	k, err := New(Config{}, nil, nil, &messenger{}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	if err := k.Submit(context.Background(), event("c", "hi"), func(otogi.Outcome) { called = true }); !errors.Is(err, otogi.ErrUnavailable) {
		t.Fatalf("Submit before Start = %v, want ErrUnavailable", err)
	}
	k = start(t, Config{}, nil, nil, nil)
	bad := event("c", "hi")
	bad.Platform = ""
	if err := k.Submit(context.Background(), bad, func(otogi.Outcome) { called = true }); !errors.Is(err, otogi.ErrInvalid) {
		t.Fatalf("Submit invalid = %v, want ErrInvalid", err)
	}
	if called {
		t.Fatal("done called although Submit returned an error")
	}
}

func TestPerConversationOrderingWithinModule(t *testing.T) {
	var mu sync.Mutex
	got := map[otogi.ConversationID][]string{}
	k := start(t, Config{Workers: 8}, []otogi.Module{module("m", func(_ context.Context, e otogi.Event, _ otogi.Invocation) error {
		time.Sleep(time.Millisecond) // widen the window for reordering
		mu.Lock()
		got[e.Conversation.ID] = append(got[e.Conversation.ID], e.Message.PlainText())
		mu.Unlock()
		return nil
	})}, nil, nil)

	var outcomes []<-chan otogi.Outcome
	for i := range 20 {
		for _, c := range []string{"c1", "c2", "c3"} {
			outcomes = append(outcomes, submit(t, k, event(c, fmt.Sprint(i))))
		}
	}
	for _, o := range outcomes {
		await(t, o)
	}
	for c, texts := range got {
		for i, text := range texts {
			if text != fmt.Sprint(i) {
				t.Fatalf("conversation %s order = %v", c, texts)
			}
		}
	}
}

func TestBlockedDeliveryDoesNotDelayOtherConversationsOrModules(t *testing.T) {
	release := make(chan struct{})
	slow := module("slow", func(ctx context.Context, e otogi.Event, _ otogi.Invocation) error {
		if e.Conversation.ID == "busy" {
			<-release
		}
		return nil
	})
	fast := module("fast", func(context.Context, otogi.Event, otogi.Invocation) error { return nil })
	k := start(t, Config{Workers: 4}, []otogi.Module{slow, fast}, nil, nil)

	blocked := submit(t, k, event("busy", "1"))
	queuedBehind := submit(t, k, event("busy", "2"))
	if o := await(t, submit(t, k, event("other", "1"))); len(o.Modules) != 2 {
		t.Fatalf("other conversation outcome = %+v", o)
	}
	select {
	case <-blocked:
		t.Fatal("blocked event completed early")
	case <-queuedBehind:
		t.Fatal("second event overtook the first in the same lane")
	default:
	}
	close(release)
	await(t, blocked)
	await(t, queuedBehind)
}

func TestOrderNoneRunsSameConversationConcurrently(t *testing.T) {
	const n = 4
	var started sync.WaitGroup
	started.Add(n)
	unordered := moduleFunc{
		descriptor: otogi.ModuleDescriptor{ID: "m", Subscribes: []otogi.EventAction{otogi.ActionMessageCreated}, Ordering: otogi.OrderNone},
		handle: func(context.Context, otogi.Event, otogi.Invocation) error {
			started.Done()
			started.Wait() // returns only once all n deliveries run at the same time
			return nil
		},
	}
	k := start(t, Config{Workers: n}, []otogi.Module{unordered}, nil, nil)
	var outcomes []<-chan otogi.Outcome
	for range n {
		outcomes = append(outcomes, submit(t, k, event("same", "x")))
	}
	for _, o := range outcomes {
		await(t, o)
	}
}

func TestFailuresAreConfinedToTheirDelivery(t *testing.T) {
	failure := errors.New("boom")
	modules := []otogi.Module{
		module("error", func(context.Context, otogi.Event, otogi.Invocation) error { return failure }),
		module("ok", func(context.Context, otogi.Event, otogi.Invocation) error { return nil }),
		module("panic", func(context.Context, otogi.Event, otogi.Invocation) error { panic("bad module") }),
		moduleFunc{
			descriptor: otogi.ModuleDescriptor{ID: "timeout", Subscribes: []otogi.EventAction{otogi.ActionMessageCreated}, Timeout: 10 * time.Millisecond},
			handle: func(ctx context.Context, _ otogi.Event, _ otogi.Invocation) error {
				<-ctx.Done()
				return ctx.Err()
			},
		},
	}
	k := start(t, Config{}, modules, nil, nil)
	o := await(t, submit(t, k, event("c", "hi")))
	results := map[otogi.ModuleID]error{}
	for _, m := range o.Modules {
		results[m.Module] = m.Err
	}
	switch {
	case !errors.Is(results["error"], failure):
		t.Errorf("error module: %v", results["error"])
	case results["ok"] != nil:
		t.Errorf("ok module: %v", results["ok"])
	case results["panic"] == nil || !strings.Contains(results["panic"].Error(), "bad module"):
		t.Errorf("panic module: %v", results["panic"])
	case !errors.Is(results["timeout"], context.DeadlineExceeded):
		t.Errorf("timeout module: %v", results["timeout"])
	}
	if o.Err != nil {
		t.Errorf("event error = %v, want nil", o.Err)
	}
}

func TestMiddlewareModifiesAndDrops(t *testing.T) {
	var got string
	m := module("m", func(_ context.Context, e otogi.Event, _ otogi.Invocation) error {
		got = e.Message.PlainText()
		return nil
	})
	rewrite := middlewareFunc(func(_ context.Context, e *otogi.Event) (otogi.Verdict, error) {
		if e.Message.PlainText() == "drop" {
			return otogi.Drop, nil
		}
		if e.Message.PlainText() == "fail" {
			return otogi.Continue, errors.New("policy store down")
		}
		e.Message = otogi.TextMessage("rewritten")
		return otogi.Continue, nil
	})
	k := start(t, Config{}, []otogi.Module{m}, []otogi.Middleware{rewrite}, nil)

	if o := await(t, submit(t, k, event("c", "hi"))); len(o.Modules) != 1 || got != "rewritten" {
		t.Fatalf("outcome %+v, module saw %q", o, got)
	}
	if o := await(t, submit(t, k, event("c", "drop"))); o.DroppedBy != "mw" || o.Err != nil || len(o.Modules) != 0 {
		t.Fatalf("drop outcome = %+v", o)
	}
	if o := await(t, submit(t, k, event("c", "fail"))); o.DroppedBy != "mw" || o.Err == nil {
		t.Fatalf("middleware error outcome = %+v", o)
	}
}

func TestSendAddressesTheEventAndRejectsReusedSlots(t *testing.T) {
	m := &messenger{}
	var reuse error
	echo := module("echo", func(ctx context.Context, e otogi.Event, inv otogi.Invocation) error {
		ref, err := inv.Send(ctx, "reply", e.Message)
		if err != nil {
			return err
		}
		if ref.SourceEvent != "sent" {
			return fmt.Errorf("Send returned %+v", ref)
		}
		_, reuse = inv.Send(ctx, "reply", e.Message)
		return nil
	})
	k := start(t, Config{}, []otogi.Module{echo}, nil, m)
	e := event("c", "hi")
	e.ID = "stable"
	await(t, submit(t, k, e))

	if len(m.sent) != 1 {
		t.Fatalf("sent %d messages", len(m.sent))
	}
	sent := m.sent[0]
	if sent.Conversation != "c" || sent.Event != "stable" || sent.Module != "echo" || sent.Key != otogi.IdempotencyKeyFor("stable", "echo", "reply") {
		t.Fatalf("outbound = %+v", sent)
	}
	if !errors.Is(reuse, otogi.ErrInvalid) {
		t.Fatalf("reused slot = %v, want ErrInvalid", reuse)
	}
}

func TestSubmitBlocksAtCapacity(t *testing.T) {
	release := make(chan struct{})
	k := start(t, Config{MaxInFlight: 1}, []otogi.Module{module("m", func(context.Context, otogi.Event, otogi.Invocation) error {
		<-release
		return nil
	})}, nil, nil)
	first := submit(t, k, event("c", "1"))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := k.Submit(ctx, event("c", "2"), nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Submit at capacity = %v, want deadline exceeded", err)
	}
	close(release)
	await(t, first)
	await(t, submit(t, k, event("c", "3")))
}

func TestDrainWaitsAndRejectsNewEvents(t *testing.T) {
	release := make(chan struct{})
	k := start(t, Config{}, []otogi.Module{module("m", func(context.Context, otogi.Event, otogi.Invocation) error {
		<-release
		return nil
	})}, nil, nil)
	pending := submit(t, k, event("c", "1"))

	drained := make(chan error, 1)
	go func() { drained <- k.Drain(context.Background()) }()
	for k.ready() {
		runtime.Gosched()
	}
	if err := k.Submit(context.Background(), event("c", "2"), nil); !errors.Is(err, otogi.ErrUnavailable) {
		t.Fatalf("Submit while draining = %v, want ErrUnavailable", err)
	}
	select {
	case err := <-drained:
		t.Fatalf("Drain returned %v before in-flight event finished", err)
	default:
	}
	close(release)
	if o := await(t, pending); o.Err != nil {
		t.Fatalf("drained event outcome = %+v", o)
	}
	if err := <-drained; err != nil {
		t.Fatal(err)
	}
}

func TestStopCompletesQueuedEventsWithError(t *testing.T) {
	entered := make(chan struct{})
	k := start(t, Config{Workers: 1}, []otogi.Module{module("m", func(ctx context.Context, _ otogi.Event, _ otogi.Invocation) error {
		entered <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	})}, nil, nil)
	running := submit(t, k, event("c", "1"))
	queued := submit(t, k, event("c", "2"))
	<-entered

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := k.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	for _, outcomes := range []<-chan otogi.Outcome{running, queued} {
		if o := await(t, outcomes); !errors.Is(o.Err, otogi.ErrUnavailable) {
			t.Fatalf("outcome after Stop = %+v, want ErrUnavailable", o)
		}
	}
}

type recorder struct {
	mu      sync.Mutex
	records []otogi.Record
}

func (r *recorder) Record(_ context.Context, rec otogi.Record) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, rec)
}

func (r *recorder) kinds(event otogi.EventID) map[string]otogi.Record {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]otogi.Record{}
	for _, rec := range r.records {
		if rec.Event == event {
			out[rec.Kind+" "+rec.Component] = rec
		}
	}
	return out
}

func TestKernelRecordsEventLog(t *testing.T) {
	rec := &recorder{}
	modules := []otogi.Module{
		module("ok", func(ctx context.Context, e otogi.Event, inv otogi.Invocation) error {
			_, err := inv.Send(ctx, "reply", otogi.TextMessage("pong"))
			return err
		}),
		module("bad", func(context.Context, otogi.Event, otogi.Invocation) error { return errors.New("boom") }),
		module("crash", func(context.Context, otogi.Event, otogi.Invocation) error { panic("oops") }),
	}
	drop := middlewareFunc(func(_ context.Context, e *otogi.Event) (otogi.Verdict, error) {
		if e.Message.PlainText() == "drop" {
			return otogi.Drop, nil
		}
		return otogi.Continue, nil
	})
	k := start(t, Config{Recorder: rec}, modules, []otogi.Middleware{drop}, nil)

	o := await(t, submit(t, k, event("c", "ping")))
	got := rec.kinds(o.EventID)
	for _, want := range []string{"event.received kernel", "module.completed module:ok", "message.sent module:ok", "module.failed module:bad", "module.panicked module:crash"} {
		if _, ok := got[want]; !ok {
			t.Errorf("missing record %q; have %v", want, keys(got))
		}
	}
	if received := got["event.received kernel"]; received.Conversation != "c" || received.Payload.(otogi.Event).Message.PlainText() != "ping" {
		t.Errorf("event.received = %+v", received)
	}
	if panicked := got["module.panicked module:crash"]; !strings.Contains(panicked.Payload.(map[string]any)["stack"].(string), "kernel_test.go") {
		t.Errorf("panic record lacks the stack: %+v", panicked.Payload)
	}

	dropped := await(t, submit(t, k, event("c", "drop")))
	if _, ok := rec.kinds(dropped.EventID)["event.dropped middleware:mw"]; !ok {
		t.Errorf("missing event.dropped; have %v", keys(rec.kinds(dropped.EventID)))
	}
}

func keys(m map[string]otogi.Record) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

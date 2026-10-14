package kernel

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/internal/adapter/modulestore"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// starterModule runs start during Start.
type starterModule struct {
	moduleFunc
	start func(otogi.Host) error
}

func (m starterModule) Start(_ context.Context, h otogi.Host) error { return m.start(h) }

func noop(context.Context, otogi.Event, otogi.Invocation) error { return nil }

func describe(id otogi.ModuleID, provides, requires []string) moduleFunc {
	return moduleFunc{descriptor: otogi.ModuleDescriptor{ID: id, Provides: provides, Requires: requires}, handle: noop}
}

type greeter interface{ Greet() string }
type english struct{}

func (english) Greet() string { return "hello" }

func TestServicesStartInDependencyOrder(t *testing.T) {
	var order []string
	var greeting string
	provider := starterModule{moduleFunc: describe("z-provider", []string{"greeter"}, nil), start: func(h otogi.Host) error {
		order = append(order, "provider")
		return h.Provide("greeter", english{})
	}}
	consumer := starterModule{moduleFunc: describe("a-consumer", nil, []string{"greeter"}), start: func(h otogi.Host) error {
		order = append(order, "consumer")
		g, err := otogi.Lookup[greeter](h, "greeter")
		if err != nil {
			return err
		}
		greeting = g.Greet()
		if _, ok := h.Service("undeclared"); ok {
			return errors.New("undeclared service was visible")
		}
		if err := h.Provide("greeter", english{}); !errors.Is(err, otogi.ErrInvalid) {
			return errors.New("provided a service it does not declare")
		}
		return nil
	}}
	start(t, Config{}, []otogi.Module{consumer, provider}, nil, nil)
	if !slices.Equal(order, []string{"provider", "consumer"}) || greeting != "hello" {
		t.Fatalf("order = %v, greeting = %q", order, greeting)
	}
}

func TestServiceGraphErrors(t *testing.T) {
	tests := map[string][]otogi.Module{
		"missing provider":   {describe("a", nil, []string{"x"})},
		"duplicate provider": {describe("a", []string{"x"}, nil), describe("b", []string{"x"}, nil)},
		"cycle":              {describe("a", []string{"x"}, []string{"y"}), describe("b", []string{"y"}, []string{"x"})},
		"self dependency":    {describe("a", []string{"x"}, []string{"x"})},
	}
	for name, modules := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := New(Config{}, modules, nil, &messenger{}, quiet); !errors.Is(err, otogi.ErrInvalid) {
				t.Fatalf("New = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestScheduledJobRuns(t *testing.T) {
	m := &messenger{}
	type run struct {
		origin otogi.Origin
		err    error
	}
	runs := make(chan run, 4)
	var mu sync.Mutex
	release := make(chan struct{})
	jobs := starterModule{moduleFunc: describe("jobs", nil, nil), start: func(h otogi.Host) error {
		if err := h.Schedule("bad", "every so often", nil); !errors.Is(err, otogi.ErrInvalid) {
			return errors.New("invalid spec accepted")
		}
		return h.Schedule("tick", "@every 1s", func(ctx context.Context, inv otogi.Invocation) error {
			_, sendErr := inv.Send(ctx, "x", otogi.TextMessage("no event to reply to"))
			_, err := inv.SendTo(ctx, "report", otogi.Destination{Platform: "test", Account: "bot", Conversation: "ops"}, otogi.TextMessage("tick"))
			mu.Lock()
			defer mu.Unlock()
			runs <- run{origin: inv.Origin(), err: errors.Join(err, expect(sendErr, otogi.ErrInvalid))}
			<-release // hold the run so the next tick finds it pending
			return nil
		})
	}}
	skipped := make(chan struct{})
	log := slog.New(&signalHandler{Handler: quiet.Handler(), message: "job skipped: previous run still pending", seen: skipped})
	k, err := New(Config{}, []otogi.Module{jobs}, nil, m, log)
	if err != nil {
		t.Fatal(err)
	}
	if err := k.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = k.Stop(context.Background()) })
	var first run
	select {
	case first = <-runs:
	case <-time.After(5 * time.Second):
		t.Fatal("job did not run")
	}
	if first.err != nil || first.origin.Job != "tick" || first.origin.Event != nil || first.origin.ScheduledAt.IsZero() {
		t.Fatalf("job run = %+v", first)
	}
	// While the run is held, the next tick is skipped rather than queued.
	select {
	case <-skipped:
	case <-time.After(5 * time.Second):
		t.Fatal("tick during a pending run was not skipped")
	}
	close(release)
	m.mu.Lock()
	sent := m.sent[0]
	m.mu.Unlock()
	if !strings.HasPrefix(sent.Origin, "job:jobs:tick:") || sent.To.Conversation != "ops" {
		t.Fatalf("job effect = %+v", sent)
	}
	if err := (&host{kernel: k, entry: k.byID["jobs"]}).Schedule("late", "@every 1s", nil); !errors.Is(err, otogi.ErrInvalid) {
		t.Fatalf("Schedule after Start = %v", err)
	}
}

func expect(err, want error) error {
	if errors.Is(err, want) {
		return nil
	}
	return errors.Join(errors.New("unexpected error"), err)
}

// timerModule records timer runs and fails the first one if failFirst.
type timerModule struct {
	starterModule
	runs      chan otogi.Timer
	failFirst bool
	mu        *sync.Mutex
	calls     *int
}

func (m timerModule) HandleTimer(_ context.Context, t otogi.Timer, inv otogi.Invocation) error {
	m.mu.Lock()
	*m.calls++
	first := *m.calls == 1
	m.mu.Unlock()
	if inv.Origin().Timer == nil || inv.Origin().Timer.Key != t.Key {
		return errors.New("origin does not carry the timer")
	}
	m.runs <- t
	if m.failFirst && first {
		return errors.New("transient")
	}
	return nil
}

func TestPersistentTimers(t *testing.T) {
	for _, failFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "success deletes", true: "failure retries later"}[failFirst], func(t *testing.T) {
			ctx := context.Background()
			storage, err := modulestore.Open(ctx, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = storage.Close() }()
			// A timer that came due while the process was down.
			if err := storage.SetTimer(ctx, "reminders", "tea", time.Now().Add(-time.Hour), []byte("payload")); err != nil {
				t.Fatal(err)
			}
			var mu sync.Mutex
			calls := 0
			m := timerModule{starterModule: starterModule{moduleFunc: describe("reminders", nil, nil), start: func(otogi.Host) error { return nil }},
				runs: make(chan otogi.Timer, 4), failFirst: failFirst, mu: &mu, calls: &calls}
			start(t, Config{Storage: storage}, []otogi.Module{m}, nil, nil)

			select {
			case got := <-m.runs:
				if got.Key != "tea" || string(got.Payload) != "payload" {
					t.Fatalf("timer = %+v", got)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("overdue timer did not fire on start")
			}
			dueLater := func(after time.Duration) int {
				n := 0
				_ = storage.DueTimers(ctx, time.Now().Add(after), []otogi.ModuleID{"reminders"}, func(otogi.ModuleID, otogi.Timer) { n++ })
				return n
			}
			eventually(t, "timer run to finish", func() bool {
				if failFirst {
					return dueLater(0) == 0 && dueLater(2*time.Minute) == 1 // postponed, kept
				}
				return dueLater(24*time.Hour) == 0 // deleted
			})
		})
	}
}

func TestSetTimerRequiresHandler(t *testing.T) {
	ctx := context.Background()
	storage, err := modulestore.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = storage.Close() }()
	var setErr error
	m := starterModule{moduleFunc: describe("plain", nil, nil), start: func(h otogi.Host) error {
		setErr = h.SetTimer(ctx, "k", time.Now(), nil)
		return nil
	}}
	start(t, Config{Storage: storage}, []otogi.Module{m}, nil, nil)
	if !errors.Is(setErr, otogi.ErrInvalid) {
		t.Fatalf("SetTimer without HandleTimer = %v", setErr)
	}
}

// eventually polls cond until it holds or the deadline passes.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// signalHandler closes seen when a record with message is logged.
type signalHandler struct {
	slog.Handler
	message string
	seen    chan struct{}
	once    sync.Once
}

func (h *signalHandler) Handle(ctx context.Context, r slog.Record) error {
	if r.Message == h.message {
		h.once.Do(func() { close(h.seen) })
	}
	return h.Handler.Handle(ctx, r)
}

// Package kernel admits events, runs middleware, and fans events out to
// modules on a bounded worker pool with per-module, per-conversation ordering.
package kernel

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// Config tunes the kernel. Zero fields take the defaults below.
type Config struct {
	// Workers is the number of goroutines running module deliveries. Default 32.
	Workers int
	// MaxInFlight bounds events admitted but not yet completed; Submit blocks
	// while it is reached. Default 1024.
	MaxInFlight int
	// HandlerTimeout bounds one delivery unless the module sets its own.
	// Default 10 minutes; negative disables the timeout.
	HandlerTimeout time.Duration
}

func (c Config) withDefaults() Config {
	if c.Workers <= 0 {
		c.Workers = 32
	}
	if c.MaxInFlight <= 0 {
		c.MaxInFlight = 1024
	}
	if c.HandlerTimeout == 0 {
		c.HandlerTimeout = 10 * time.Minute
	}
	return c
}

type state int

const (
	stateNew state = iota
	stateReady
	stateDraining
	stateStopped
)

type moduleEntry struct {
	module     otogi.Module
	descriptor otogi.ModuleDescriptor
	subscribes map[otogi.EventAction]bool
	timeout    time.Duration
}

// Kernel routes events to modules. Create it with New, then Start, Submit
// events, and finally Drain and Stop.
type Kernel struct {
	cfg        Config
	log        *slog.Logger
	messenger  otogi.Messenger
	middleware []otogi.Middleware
	modules    []*moduleEntry
	slots      chan struct{}

	mu       sync.Mutex
	state    state
	inFlight sync.WaitGroup
	runCtx   context.Context
	cancel   context.CancelFunc
	sched    scheduler
	workers  sync.WaitGroup
}

// New validates the module set and returns a kernel that is not yet started.
// Modules are run in module-ID order where order is observable (outcomes).
func New(cfg Config, modules []otogi.Module, middleware []otogi.Middleware, messenger otogi.Messenger, log *slog.Logger) (*Kernel, error) {
	if messenger == nil {
		return nil, fmt.Errorf("new kernel: nil messenger: %w", otogi.ErrInvalid)
	}
	if log == nil {
		log = slog.Default()
	}
	cfg = cfg.withDefaults()
	entries := make([]*moduleEntry, 0, len(modules))
	seen := make(map[otogi.ModuleID]bool, len(modules))
	for _, module := range modules {
		d := module.Descriptor()
		if d.ID == "" || seen[d.ID] {
			return nil, fmt.Errorf("new kernel: empty or duplicate module ID %q: %w", d.ID, otogi.ErrInvalid)
		}
		seen[d.ID] = true
		entry := &moduleEntry{module: module, descriptor: d, subscribes: make(map[otogi.EventAction]bool, len(d.Subscribes)), timeout: cfg.HandlerTimeout}
		for _, action := range d.Subscribes {
			if !action.Valid() {
				return nil, fmt.Errorf("new kernel: module %q subscribes to invalid action %q: %w", d.ID, action, otogi.ErrInvalid)
			}
			entry.subscribes[action] = true
		}
		if d.Timeout != 0 {
			entry.timeout = d.Timeout
		}
		entries = append(entries, entry)
	}
	slices.SortFunc(entries, func(a, b *moduleEntry) int { return strings.Compare(string(a.descriptor.ID), string(b.descriptor.ID)) })
	k := &Kernel{
		cfg: cfg, log: log, messenger: messenger, modules: entries,
		middleware: slices.Clone(middleware),
		slots:      make(chan struct{}, cfg.MaxInFlight),
	}
	k.sched.init(&k.mu)
	return k, nil
}

// Start starts the worker pool. ctx is not retained; Stop ends the workers.
func (k *Kernel) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.state != stateNew {
		return fmt.Errorf("start kernel: already started: %w", otogi.ErrUnavailable)
	}
	k.runCtx, k.cancel = context.WithCancel(context.Background())
	for range k.cfg.Workers {
		k.workers.Go(k.work)
	}
	k.state = stateReady
	return nil
}

// Submit admits event and returns once it is queued for the modules, not once
// they have finished. If Submit returns nil, done is called exactly once, from
// a kernel goroutine or before Submit returns, when every delivery has
// finished or the event was dropped; done must not block for long. If Submit
// returns an error, done is never called.
//
// Submit validates the event, assigns an ID if it has none, and blocks under
// ctx while MaxInFlight events are in flight. Middleware runs synchronously
// inside Submit, in registration order.
func (k *Kernel) Submit(ctx context.Context, event otogi.Event, done func(otogi.Outcome)) error {
	if err := event.Validate(); err != nil {
		return fmt.Errorf("submit: %w", err)
	}
	if event.ID == "" {
		id, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("submit: generate event ID: %w", err)
		}
		event.ID = otogi.EventID(id.String())
	}
	if err := k.admit(ctx); err != nil {
		return err
	}
	tracked := &tracked{kernel: k, event: event, done: done}
	for _, mw := range k.middleware {
		verdict, err := k.intercept(ctx, mw, &tracked.event)
		if err != nil || verdict == otogi.Drop {
			k.log.Info("event dropped by middleware", "event", tracked.event.ID, "middleware", mw.ID(), "error", err)
			tracked.dropped(mw.ID(), err)
			return nil
		}
	}
	var deliveries []*delivery
	for i, entry := range k.modules {
		if entry.subscribes[tracked.event.Action] {
			deliveries = append(deliveries, &delivery{event: tracked, module: entry, index: i})
		}
	}
	tracked.begin(len(deliveries))
	k.mu.Lock()
	for _, d := range deliveries {
		k.sched.enqueue(d)
	}
	k.mu.Unlock()
	return nil
}

func (k *Kernel) admit(ctx context.Context) error {
	if !k.ready() {
		return fmt.Errorf("submit: kernel not ready: %w", otogi.ErrUnavailable)
	}
	select {
	case k.slots <- struct{}{}:
	case <-ctx.Done():
		return fmt.Errorf("submit: waiting for capacity: %w", ctx.Err())
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.state != stateReady {
		<-k.slots
		return fmt.Errorf("submit: kernel not ready: %w", otogi.ErrUnavailable)
	}
	k.inFlight.Add(1)
	return nil
}

func (k *Kernel) ready() bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.state == stateReady
}

func (k *Kernel) intercept(ctx context.Context, mw otogi.Middleware, event *otogi.Event) (verdict otogi.Verdict, err error) {
	defer func() {
		if r := recover(); r != nil {
			k.log.Error("middleware panicked", "middleware", mw.ID(), "event", event.ID, "panic", r, "stack", string(debug.Stack()))
			err = fmt.Errorf("middleware %q panicked: %v", mw.ID(), r)
		}
	}()
	return mw.Intercept(ctx, event)
}

// Drain stops admitting events and waits until every admitted event has
// completed, or ctx ends.
func (k *Kernel) Drain(ctx context.Context) error {
	k.mu.Lock()
	if k.state == stateReady {
		k.state = stateDraining
	}
	k.mu.Unlock()
	return wait(ctx, &k.inFlight)
}

// Stop stops admission, cancels running deliveries, completes queued ones
// without running them (their outcomes carry an error), and waits for the
// workers to exit or ctx to end. It is safe to call more than once.
func (k *Kernel) Stop(ctx context.Context) error {
	k.mu.Lock()
	if k.state == stateNew || k.state == stateStopped {
		k.state = stateStopped
		k.mu.Unlock()
		return nil
	}
	k.state = stateStopped
	k.cancel()
	k.sched.stop()
	k.mu.Unlock()
	return wait(ctx, &k.workers)
}

func wait(ctx context.Context, wg *sync.WaitGroup) error {
	finished := make(chan struct{})
	go func() {
		wg.Wait()
		close(finished)
	}()
	select {
	case <-finished:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// tracked is one admitted event and its pending deliveries.
type tracked struct {
	kernel  *Kernel
	event   otogi.Event
	done    func(otogi.Outcome)
	mu      sync.Mutex
	pending int
	outcome otogi.Outcome
}

func (t *tracked) begin(deliveries int) {
	t.outcome = otogi.Outcome{EventID: t.event.ID, Modules: make([]otogi.ModuleOutcome, 0, deliveries)}
	t.pending = deliveries
	if deliveries == 0 {
		t.finish()
	}
}

func (t *tracked) dropped(by otogi.MiddlewareID, err error) {
	t.outcome = otogi.Outcome{EventID: t.event.ID, DroppedBy: by, Err: err}
	t.finish()
}

func (t *tracked) record(outcome otogi.ModuleOutcome) {
	t.mu.Lock()
	t.outcome.Modules = append(t.outcome.Modules, outcome)
	t.pending--
	last := t.pending == 0
	t.mu.Unlock()
	if last {
		slices.SortFunc(t.outcome.Modules, func(a, b otogi.ModuleOutcome) int { return strings.Compare(string(a.Module), string(b.Module)) })
		t.finish()
	}
}

func (t *tracked) finish() {
	k := t.kernel
	if k.runCtx.Err() != nil && t.outcome.Err == nil {
		t.outcome.Err = fmt.Errorf("kernel stopped before the event completed: %w", otogi.ErrUnavailable)
	}
	if t.done != nil {
		t.done(t.outcome)
	}
	<-k.slots
	k.inFlight.Done()
}

// run executes one delivery on a worker goroutine.
func (k *Kernel) run(d *delivery) {
	started := time.Now()
	err := k.invoke(d)
	outcome := otogi.ModuleOutcome{Module: d.module.descriptor.ID, Err: err, Duration: time.Since(started)}
	if err != nil {
		k.log.Error("module failed", "event", d.event.event.ID, "module", outcome.Module, "duration", outcome.Duration, "error", err)
	} else {
		k.log.Debug("module completed", "event", d.event.event.ID, "module", outcome.Module, "duration", outcome.Duration)
	}
	d.event.record(outcome)
}

func (k *Kernel) invoke(d *delivery) (err error) {
	if err := k.runCtx.Err(); err != nil {
		return fmt.Errorf("not run: kernel stopped: %w", err)
	}
	ctx, cancel := k.runCtx, context.CancelFunc(func() {})
	if d.module.timeout > 0 {
		ctx, cancel = context.WithTimeout(k.runCtx, d.module.timeout)
	}
	defer cancel()
	defer func() {
		if r := recover(); r != nil {
			k.log.Error("module panicked", "event", d.event.event.ID, "module", d.module.descriptor.ID, "panic", r, "stack", string(debug.Stack()))
			err = fmt.Errorf("module %q panicked: %v", d.module.descriptor.ID, r)
		}
	}()
	return d.module.module.Handle(ctx, d.event.event, &invocation{kernel: k, event: d.event.event, module: d.module.descriptor.ID})
}

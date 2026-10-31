// Package kernel admits events, runs middleware, and fans events out to
// modules on a bounded worker pool with per-module, per-conversation ordering.
package kernel

import (
	"context"
	"errors"
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
	// Recorder receives the event log records the kernel writes (see
	// docs/observability.md). Default: discard.
	Recorder otogi.Recorder
	// Storage backs module stores and private databases. Without it, module
	// storage calls return ErrUnavailable.
	Storage ModuleStorage
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
	if c.Recorder == nil {
		c.Recorder = otogi.NopRecorder{}
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
	host       *host
	provides   map[string]bool
	requires   map[string]bool
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
	effector   otogi.Effector
	middleware []otogi.Middleware
	modules    []*moduleEntry
	startOrder []*moduleEntry // providers before the modules requiring them
	byID       map[otogi.ModuleID]*moduleEntry
	commands   map[string][]*moduleEntry // command name -> declaring module
	slots      chan struct{}

	mu       sync.Mutex
	state    state
	inFlight sync.WaitGroup
	runCtx   context.Context
	cancel   context.CancelFunc
	sched    scheduler
	workers  sync.WaitGroup
	starting bool // modules may Schedule and Provide only while starting
	services map[string]any
	jobs     []*job
	timers   map[string]bool // module/key of timers running now
}

// New validates the module set and returns a kernel that is not yet started.
// Modules are run in module-ID order where order is observable (outcomes).
func New(cfg Config, modules []otogi.Module, middleware []otogi.Middleware, effector otogi.Effector, log *slog.Logger) (*Kernel, error) {
	if effector == nil {
		return nil, fmt.Errorf("new kernel: nil effector: %w", otogi.ErrInvalid)
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
		entry := &moduleEntry{module: module, descriptor: d, subscribes: make(map[otogi.EventAction]bool, len(d.Subscribes)), timeout: cfg.HandlerTimeout, provides: set(d.Provides), requires: set(d.Requires)}
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
	commands := map[string][]*moduleEntry{}
	for _, entry := range entries {
		for _, c := range entry.descriptor.Commands {
			if !commandName.MatchString(c.Name) {
				return nil, fmt.Errorf("new kernel: module %q command %q: want lowercase letters, digits, or _: %w", entry.descriptor.ID, c.Name, otogi.ErrInvalid)
			}
			if owner := commands[c.Name]; owner != nil {
				return nil, fmt.Errorf("new kernel: command %q declared by both %q and %q: %w", c.Name, owner[0].descriptor.ID, entry.descriptor.ID, otogi.ErrInvalid)
			}
			commands[c.Name] = []*moduleEntry{entry}
		}
	}
	order, err := startOrder(entries)
	if err != nil {
		return nil, fmt.Errorf("new kernel: %w", err)
	}
	k := &Kernel{
		cfg: cfg, log: log, effector: effector, modules: entries, startOrder: order, commands: commands,
		byID:       map[otogi.ModuleID]*moduleEntry{},
		middleware: slices.Clone(middleware),
		slots:      make(chan struct{}, cfg.MaxInFlight),
		services:   map[string]any{},
		timers:     map[string]bool{},
	}
	k.sched.init(&k.mu)
	for _, entry := range entries {
		entry.host = &host{kernel: k, entry: entry, log: log.With("module", entry.descriptor.ID)}
		k.byID[entry.descriptor.ID] = entry
	}
	return k, nil
}

// Start starts every module implementing otogi.Starter, then the worker
// pool. If a module fails to start, the modules already started are stopped
// and the error is returned. ctx is not retained; Stop ends the workers.
func (k *Kernel) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	k.mu.Lock()
	if k.state != stateNew {
		k.mu.Unlock()
		return fmt.Errorf("start kernel: already started: %w", otogi.ErrUnavailable)
	}
	k.starting = true
	k.mu.Unlock()
	for i, entry := range k.startOrder {
		starter, ok := entry.module.(otogi.Starter)
		if !ok {
			continue
		}
		if err := guard(func() error { return starter.Start(ctx, entry.host) }); err != nil {
			k.mu.Lock()
			k.starting = false
			k.mu.Unlock()
			stopErr := k.stopModules(ctx, k.startOrder[:i])
			return errors.Join(fmt.Errorf("start module %q: %w", entry.descriptor.ID, err), stopErr)
		}
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	k.starting = false
	k.runCtx, k.cancel = context.WithCancel(context.Background())
	for range k.cfg.Workers {
		k.workers.Go(k.work)
	}
	k.workers.Go(k.clock)
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
	k.record(event, "event.received", "kernel", "", 0, event)
	tracked := &tracked{kernel: k, event: event, done: done}
	for _, mw := range k.middleware {
		verdict, err := k.intercept(ctx, mw, &tracked.event)
		if err != nil || verdict == otogi.Drop {
			k.log.Info("event dropped by middleware", "event", tracked.event.ID, "middleware", mw.ID(), "error", err)
			k.record(tracked.event, "event.dropped", "middleware:"+string(mw.ID()), "dropped", 0, map[string]any{"middleware": mw.ID(), "error": errorText(err)})
			tracked.dropped(mw.ID(), err)
			return nil
		}
	}
	var deliveries []*delivery
	for _, entry := range k.recipients(tracked.event) {
		deliveries = append(deliveries, eventDelivery(tracked, entry))
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
	if err := wait(ctx, &k.workers); err != nil {
		return err
	}
	return k.stopModules(ctx, k.startOrder)
}

// stopModules stops modules implementing otogi.Stopper, in reverse order.
func (k *Kernel) stopModules(ctx context.Context, entries []*moduleEntry) error {
	var errs []error
	for _, entry := range slices.Backward(entries) {
		if stopper, ok := entry.module.(otogi.Stopper); ok {
			if err := guard(func() error { return stopper.Stop(ctx) }); err != nil {
				errs = append(errs, fmt.Errorf("stop module %q: %w", entry.descriptor.ID, err))
			}
		}
	}
	return errors.Join(errs...)
}

// guard turns a panic in fn into an error.
func guard(fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v\n%s", r, debug.Stack())
		}
	}()
	return fn()
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

// eventDelivery is one event for one module, in the module's lane for the
// event's conversation.
func eventDelivery(t *tracked, entry *moduleEntry) *delivery {
	e := t.event
	return &delivery{
		module:   entry,
		lane:     laneKey{module: entry.descriptor.ID, platform: e.Platform, account: e.Account, conversation: e.Conversation.ID},
		scope:    e,
		origin:   otogi.Origin{Event: &t.event},
		originID: string(e.ID),
		call: func(ctx context.Context, inv *invocation) error {
			return entry.module.Handle(ctx, e, inv)
		},
		done: t.record,
	}
}

// run executes one delivery on a worker goroutine.
func (k *Kernel) run(d *delivery) {
	started := time.Now()
	err := k.invoke(d)
	outcome := otogi.ModuleOutcome{Module: d.module.descriptor.ID, Err: err, Duration: time.Since(started)}
	component := "module:" + string(outcome.Module)
	var panicked *panicError
	switch {
	case errors.As(err, &panicked):
		k.record(d.scope, "module.panicked", component, "error", outcome.Duration, map[string]any{"origin": d.originID, "error": err.Error(), "stack": panicked.stack})
	case err != nil:
		k.log.Error("module failed", "origin", d.originID, "module", outcome.Module, "duration", outcome.Duration, "error", err)
		k.record(d.scope, "module.failed", component, "error", outcome.Duration, map[string]any{"origin": d.originID, "error": err.Error()})
	default:
		k.log.Debug("module completed", "origin", d.originID, "module", outcome.Module, "duration", outcome.Duration)
		k.record(d.scope, "module.completed", component, "ok", outcome.Duration, map[string]any{"origin": d.originID})
	}
	if d.done != nil {
		d.done(outcome)
	}
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
	work := otogi.Work{Module: d.module.descriptor.ID, Origin: d.originID, Event: d.scope.ID}
	if d.scope.ID != "" {
		work.Destination = d.scope.Destination()
	}
	ctx = otogi.ContextWithWork(ctx, work)
	inv := &invocation{kernel: k, event: d.scope, origin: d.origin, originID: d.originID, module: d.module.descriptor.ID, host: d.module.host, streams: &streams{}}
	defer inv.closeAbandoned()
	defer func() {
		if r := recover(); r != nil {
			p := &panicError{module: d.module.descriptor.ID, value: r, stack: string(debug.Stack())}
			k.log.Error("module panicked", "origin", d.originID, "module", p.module, "panic", r, "stack", p.stack)
			err = p
		}
	}()
	return d.call(ctx, inv)
}

// panicError is a recovered module panic, with its stack.
type panicError struct {
	module otogi.ModuleID
	value  any
	stack  string
}

func (p *panicError) Error() string { return fmt.Sprintf("module %q panicked: %v", p.module, p.value) }

// record writes an event log record scoped to event. Records are written
// with a background context: they must not be lost to a delivery's timeout.
func (k *Kernel) record(event otogi.Event, kind, component, outcome string, d time.Duration, payload any) {
	k.cfg.Recorder.Record(context.Background(), otogi.Record{
		At: time.Now(), Kind: kind, Component: component, Event: event.ID,
		Platform: event.Platform, Account: event.Account, Conversation: event.Conversation.ID,
		Outcome: outcome, Duration: d, Payload: payload,
	})
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// Stats is a snapshot of the kernel for status reporting.
type Stats struct {
	State       string `json:"state"`
	InFlight    int    `json:"in_flight"`
	MaxInFlight int    `json:"max_in_flight"`
	Workers     int    `json:"workers"`
}

// Stats returns the kernel's current state and load.
func (k *Kernel) Stats() Stats {
	k.mu.Lock()
	state := k.state
	k.mu.Unlock()
	return Stats{
		State:    [...]string{stateNew: "new", stateReady: "ready", stateDraining: "draining", stateStopped: "stopped"}[state],
		InFlight: len(k.slots), MaxInFlight: k.cfg.MaxInFlight, Workers: k.cfg.Workers,
	}
}

// recipients returns the modules an event goes to. A command declared by a
// module goes only to that module, whatever it subscribes to; otherwise the
// event goes to every module subscribed to its action. Filters apply in both
// cases.
func (k *Kernel) recipients(e otogi.Event) []*moduleEntry {
	if e.Command != nil {
		if owners, ok := k.commands[e.Command.Name]; ok {
			return filtered(owners, e, func(*moduleEntry) bool { return true })
		}
	}
	return filtered(k.modules, e, func(entry *moduleEntry) bool { return entry.subscribes[e.Action] })
}

func filtered(entries []*moduleEntry, e otogi.Event, eligible func(*moduleEntry) bool) []*moduleEntry {
	var out []*moduleEntry
	for _, entry := range entries {
		if eligible(entry) && entry.descriptor.Filter.Matches(e) {
			out = append(out, entry)
		}
	}
	return out
}

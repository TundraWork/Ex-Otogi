// Package kernel provides bounded in-memory event admission, routing, and lifecycle management.
package kernel

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sort"
	"sync"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

type state uint8

const (
	stateNew state = iota
	stateStarting
	stateReady
	stateDraining
	stateStopped
	stateFailed
)

// Kernel owns one bounded event worker and its lifecycle.
type Kernel struct {
	mu               sync.Mutex
	state            state
	terminalErr      error
	config           Config
	modules          []otogi.Module
	kinds            map[otogi.EventKind]struct{}
	messenger        otogi.Messenger
	generator        *otogi.EventIDGenerator
	queue            chan event
	workerCtx        context.Context
	cancel           context.CancelFunc
	wake             chan struct{}
	done             chan struct{}
	drain            bool
	drainInterrupted bool
	deliveries       []Delivery
	panics           []PanicReport
}

// New validates dependencies and module descriptors and returns an inert kernel.
func New(config Config, modules []otogi.Module, messenger otogi.Messenger, generator *otogi.EventIDGenerator) (*Kernel, error) {
	if config.queueCapacity < 1 || config.queueCapacity > 1024 || len(modules) == 0 || messenger == nil || generator == nil {
		return nil, fmt.Errorf("new kernel: %w", otogi.ErrInvalid)
	}
	owned := append([]otogi.Module(nil), modules...)
	seen := make(map[otogi.ModuleID]struct{}, len(owned))
	kinds := make(map[otogi.EventKind]struct{})
	for _, module := range owned {
		if module == nil {
			return nil, fmt.Errorf("new kernel: %w", otogi.ErrInvalid)
		}
		descriptor := module.Descriptor()
		if descriptor.ID().String() == "" || len(descriptor.Consumes()) == 0 {
			return nil, fmt.Errorf("new kernel: %w", otogi.ErrInvalid)
		}
		if _, exists := seen[descriptor.ID()]; exists {
			return nil, fmt.Errorf("new kernel duplicate module: %w", otogi.ErrInvalid)
		}
		seen[descriptor.ID()] = struct{}{}
		for _, kind := range descriptor.Consumes() {
			if kind.String() == "" {
				return nil, fmt.Errorf("new kernel: %w", otogi.ErrInvalid)
			}
			kinds[kind] = struct{}{}
		}
	}
	sort.Slice(owned, func(i, j int) bool { return owned[i].Descriptor().ID().String() < owned[j].Descriptor().ID().String() })
	return &Kernel{state: stateNew, config: config, modules: owned, kinds: kinds, messenger: messenger, generator: generator, queue: make(chan event, config.queueCapacity), wake: make(chan struct{}, 1), done: make(chan struct{})}, nil
}

// Start transitions a new kernel to ready and starts its sole worker.
func (k *Kernel) Start(ctx context.Context) error {
	k.mu.Lock()
	if k.state != stateNew {
		k.mu.Unlock()
		return fmt.Errorf("start kernel: %w", otogi.ErrInvalidState)
	}
	k.state = stateStarting
	if err := ctx.Err(); err != nil {
		k.state = stateFailed
		k.terminalErr = err
		close(k.done)
		k.mu.Unlock()
		return err
	}
	k.workerCtx, k.cancel = context.WithCancel(context.Background())
	k.state = stateReady
	k.mu.Unlock()
	go k.run()
	return nil
}

// Submit admits one supported event without blocking and returns its runtime ID.
func (k *Kernel) Submit(ctx context.Context, input otogi.EventInput) (otogi.EventID, error) {
	if err := ctx.Err(); err != nil {
		return otogi.EventID{}, err
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.state != stateReady {
		return otogi.EventID{}, fmt.Errorf("submit event: %w", otogi.ErrInvalidState)
	}
	if _, supported := k.kinds[input.Kind()]; !supported {
		return otogi.EventID{}, fmt.Errorf("submit event kind: %w", otogi.ErrInvalid)
	}
	id, err := k.generator.Next()
	if err != nil {
		return otogi.EventID{}, fmt.Errorf("submit event ID: %w", err)
	}
	select {
	case k.queue <- event{id: id, input: input}:
		return id, nil
	default:
		return otogi.EventID{}, fmt.Errorf("submit event: %w", otogi.ErrCapacity)
	}
}

// Ready reports whether the kernel currently accepts submissions.
func (k *Kernel) Ready() bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.state == stateReady
}

// Drain prevents admission and waits for all accepted events to finish.
func (k *Kernel) Drain(ctx context.Context) error {
	k.mu.Lock()
	if k.state != stateReady {
		err := k.terminalErr
		k.mu.Unlock()
		if err != nil {
			return err
		}
		return fmt.Errorf("drain kernel: %w", otogi.ErrInvalidState)
	}
	k.state = stateDraining
	k.drain = true
	select {
	case k.wake <- struct{}{}:
	default:
	}
	k.mu.Unlock()
	if err := k.wait(ctx); err != nil {
		k.mu.Lock()
		if k.state == stateDraining {
			k.state = stateFailed
			if k.terminalErr == nil {
				k.terminalErr = err
			}
			if k.cancel != nil {
				k.cancel()
			}
		}
		retained := k.terminalErr
		k.mu.Unlock()
		if retained != nil {
			return retained
		}
		return err
	}
	k.mu.Lock()
	err := k.terminalErr
	if err == nil && k.drainInterrupted {
		err = context.Canceled
	}
	k.mu.Unlock()
	return err
}

// Stop prevents admission, cancels active work, and waits for worker termination through ctx.
func (k *Kernel) Stop(ctx context.Context) error {
	k.mu.Lock()
	if k.state == stateNew {
		k.state = stateStopped
		close(k.done)
		k.mu.Unlock()
		return nil
	}
	if k.state != stateStopped {
		if k.state == stateDraining {
			k.drainInterrupted = true
		}
		if k.cancel != nil {
			k.cancel()
		}
		k.state = stateStopped
	}
	k.mu.Unlock()
	if err := k.wait(ctx); err != nil {
		k.mu.Lock()
		if k.terminalErr == nil {
			k.terminalErr = err
		}
		retained := k.terminalErr
		k.mu.Unlock()
		return retained
	}
	k.mu.Lock()
	err := k.terminalErr
	k.mu.Unlock()
	return err
}

// Wait observes worker termination and returns the retained terminal cause.
func (k *Kernel) Wait(ctx context.Context) error {
	if err := k.wait(ctx); err != nil {
		return err
	}
	k.mu.Lock()
	err := k.terminalErr
	k.mu.Unlock()
	return err
}

func (k *Kernel) wait(ctx context.Context) error {
	select {
	case <-k.done:
		return nil
	default:
	}
	select {
	case <-k.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (k *Kernel) run() {
	defer close(k.done)
	for {
		select {
		case <-k.workerCtx.Done():
			return
		case admitted := <-k.queue:
			k.route(k.workerCtx, admitted)
		case <-k.wake:
		}
		k.mu.Lock()
		drained := k.drain && len(k.queue) == 0
		if drained {
			k.state = stateStopped
		}
		k.mu.Unlock()
		if drained {
			return
		}
	}
}

func (k *Kernel) route(ctx context.Context, admitted event) {
	for _, module := range k.modules {
		interested := false
		for _, kind := range module.Descriptor().Consumes() {
			if kind == admitted.Kind() {
				interested = true
				break
			}
		}
		if !interested {
			continue
		}
		moduleID := module.Descriptor().ID()
		invocation := newInvocation(ctx, k.messenger, admitted, moduleID)
		result, report := invokeModule(ctx, module, admitted, invocation)
		result = validModuleResult(result)
		k.mu.Lock()
		if report != nil {
			k.panics = appendBounded(k.panics, *report)
		}
		k.deliveries = appendBounded(k.deliveries, Delivery{eventID: admitted.ID(), moduleID: moduleID, status: result.Status(), err: stableError(result.Err())})
		k.mu.Unlock()
	}
}

func invokeModule(ctx context.Context, module otogi.Module, admitted event, invocation *invocation) (result otogi.Result, report *PanicReport) {
	defer invocation.close()
	defer func() {
		if recover() != nil {
			stack := debug.Stack()
			if len(stack) > 32*1024 {
				stack = stack[:32*1024]
			}
			result = otogi.NewFailedResult(otogi.ErrModulePanic)
			report = &PanicReport{eventID: admitted.ID(), moduleID: module.Descriptor().ID(), stack: append([]byte(nil), stack...)}
		}
	}()
	return module.Handle(ctx, admitted, invocation), nil
}

func validModuleResult(result otogi.Result) otogi.Result {
	status := result.Status()
	err := result.Err()
	valid := (status == otogi.ResultSucceeded || status == otogi.ResultRejected) && err == nil
	valid = valid || (status == otogi.ResultFailed || status == otogi.ResultCanceled) && err != nil
	if !valid {
		return otogi.NewFailedResult(otogi.ErrInvalid)
	}
	return result
}

func stableError(err error) error {
	for _, stable := range []error{otogi.ErrModulePanic, otogi.ErrUnavailable, otogi.ErrCapacity, otogi.ErrInvalidState, otogi.ErrInvalid, context.Canceled, context.DeadlineExceeded} {
		if errors.Is(err, stable) {
			return stable
		}
	}
	if err != nil {
		return otogi.ErrUnavailable
	}
	return nil
}

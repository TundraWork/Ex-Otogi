package kernel

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"ex-otogi/pkg/otogi"
)

// EventBus is the kernel asynchronous pub/sub implementation.
type EventBus struct {
	mu                    sync.RWMutex
	nextID                int64
	closed                bool
	subscriptions         map[int64]*busSubscription
	defaultBuffer         int
	defaultWorkers        int
	defaultHandlerTimeout time.Duration
	onAsyncError          func(context.Context, string, error)
}

// NewEventBus creates an asynchronous event bus with bounded queues.
func NewEventBus(
	defaultBuffer int,
	defaultWorkers int,
	defaultHandlerTimeout time.Duration,
	onAsyncError func(context.Context, string, error),
) *EventBus {
	return &EventBus{
		subscriptions:         make(map[int64]*busSubscription),
		defaultBuffer:         defaultBuffer,
		defaultWorkers:        defaultWorkers,
		defaultHandlerTimeout: defaultHandlerTimeout,
		onAsyncError:          onAsyncError,
	}
}

// Publish dispatches an event to all matching subscribers.
func (b *EventBus) Publish(ctx context.Context, event *otogi.Event) error {
	if err := event.Validate(); err != nil {
		return fmt.Errorf("publish event %s: %w", event.Kind, err)
	}

	subs, err := b.snapshotSubscriptions()
	if err != nil {
		return fmt.Errorf("publish event %s: %w", event.Kind, err)
	}

	var publishErrs []error
	for _, sub := range subs {
		if !sub.interest.Matches(event) {
			continue
		}
		if err := sub.enqueue(ctx, event); err != nil {
			if errors.Is(err, otogi.ErrEventDropped) || errors.Is(err, otogi.ErrSubscriptionClosed) {
				b.reportAsyncError(ctx, sub.spec.Name, err)
				continue
			}
			publishErrs = append(publishErrs, err)
		}
	}

	if len(publishErrs) > 0 {
		return fmt.Errorf("publish event %s: %w", event.Kind, errors.Join(publishErrs...))
	}

	return nil
}

// Subscribe registers a bounded asynchronous consumer.
func (b *EventBus) Subscribe(
	ctx context.Context,
	interest otogi.InterestSet,
	spec otogi.SubscriptionSpec,
	handler otogi.EventHandler,
) (otogi.Subscription, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("subscribe %s: %w", spec.Name, err)
	}
	if handler == nil {
		return nil, fmt.Errorf("subscribe %s: nil handler", spec.Name)
	}

	subID := atomic.AddInt64(&b.nextID, 1)
	spec = b.normalizeSpec(spec, subID)
	sub := newBusSubscription(subID, interest, spec, handler, b)

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		sub.signalClose()
		return nil, fmt.Errorf("subscribe %s: bus closed", spec.Name)
	}
	b.subscriptions[subID] = sub

	return sub, nil
}

// Close stops all active subscriptions and rejects further publishes/subscribes.
func (b *EventBus) Close(ctx context.Context) error {
	subs := make([]*busSubscription, 0)

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	for _, sub := range b.subscriptions {
		subs = append(subs, sub)
	}
	b.subscriptions = make(map[int64]*busSubscription)
	b.mu.Unlock()

	var closeErrs []error
	for _, sub := range subs {
		if err := sub.shutdown(ctx); err != nil {
			closeErrs = append(closeErrs, err)
		}
	}

	if len(closeErrs) > 0 {
		return fmt.Errorf("close event bus: %w", errors.Join(closeErrs...))
	}

	return nil
}

// snapshotSubscriptions returns a stable copy for lock-free publish fan-out.
// It fails when the bus is closed to prevent post-shutdown dispatch.
func (b *EventBus) snapshotSubscriptions() ([]*busSubscription, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.closed {
		return nil, fmt.Errorf("bus closed")
	}

	subs := make([]*busSubscription, 0, len(b.subscriptions))
	for _, sub := range b.subscriptions {
		subs = append(subs, sub)
	}

	return subs, nil
}

// normalizeSpec applies runtime defaults when callers omit optional fields.
func (b *EventBus) normalizeSpec(spec otogi.SubscriptionSpec, subID int64) otogi.SubscriptionSpec {
	if spec.Name == "" {
		spec.Name = fmt.Sprintf("subscription-%d", subID)
	}
	if spec.Buffer <= 0 {
		spec.Buffer = b.defaultBuffer
	}
	if spec.Workers <= 0 {
		spec.Workers = b.defaultWorkers
	}
	if spec.HandlerTimeout <= 0 {
		spec.HandlerTimeout = b.defaultHandlerTimeout
	}
	if spec.Backpressure == "" {
		spec.Backpressure = otogi.BackpressureDropNewest
	}

	return spec
}

// unsubscribe removes and shuts down a subscription by id.
func (b *EventBus) unsubscribe(ctx context.Context, subID int64) error {
	b.mu.Lock()
	sub, found := b.subscriptions[subID]
	if found {
		delete(b.subscriptions, subID)
	}
	b.mu.Unlock()

	if !found {
		return nil
	}

	if err := sub.shutdown(ctx); err != nil {
		return fmt.Errorf("unsubscribe %s: %w", sub.spec.Name, err)
	}

	return nil
}

// reportAsyncError forwards background worker failures to the configured error sink.
func (b *EventBus) reportAsyncError(ctx context.Context, scope string, err error) {
	if b.onAsyncError != nil {
		b.onAsyncError(ctx, scope, err)
	}
}

// busSubscription owns queueing and worker lifecycle for a single subscriber.
// Queue closure is driven by context cancellation rather than channel close.
type busSubscription struct {
	id       int64
	interest otogi.InterestSet
	spec     otogi.SubscriptionSpec
	handler  otogi.EventHandler
	queue    chan *otogi.Event
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}
	closed   atomic.Bool
	once     sync.Once
	bus      *EventBus
}

// newBusSubscription creates and starts workers immediately.
func newBusSubscription(
	subID int64,
	interest otogi.InterestSet,
	spec otogi.SubscriptionSpec,
	handler otogi.EventHandler,
	bus *EventBus,
) *busSubscription {
	subCtx, cancel := context.WithCancel(context.Background())
	sub := &busSubscription{
		id:       subID,
		interest: cloneInterestSet(interest),
		spec:     spec,
		handler:  handler,
		queue:    make(chan *otogi.Event, spec.Buffer),
		ctx:      subCtx,
		cancel:   cancel,
		done:     make(chan struct{}),
		bus:      bus,
	}

	sub.startWorkers()

	return sub
}

// cloneInterestSet copies owned slices so caller mutation does not affect matching.
func cloneInterestSet(interest otogi.InterestSet) otogi.InterestSet {
	cloned := interest
	if len(interest.Kinds) > 0 {
		cloned.Kinds = append([]otogi.EventKind(nil), interest.Kinds...)
	}
	if len(interest.MediaTypes) > 0 {
		cloned.MediaTypes = append([]otogi.MediaType(nil), interest.MediaTypes...)
	}

	return cloned
}

// Name returns the stable subscription name.
func (s *busSubscription) Name() string {
	return s.spec.Name
}

// Close unregisters this subscription from its parent bus.
func (s *busSubscription) Close(ctx context.Context) error {
	return s.bus.unsubscribe(ctx, s.id)
}

// enqueue applies the configured backpressure policy for the subscriber queue.
func (s *busSubscription) enqueue(ctx context.Context, event *otogi.Event) error {
	if s.closed.Load() {
		return fmt.Errorf("enqueue %s: %w", s.spec.Name, otogi.ErrSubscriptionClosed)
	}

	switch s.spec.Backpressure {
	case otogi.BackpressureDropNewest:
		return s.enqueueDropNewest(event)
	case otogi.BackpressureDropOldest:
		return s.enqueueDropOldest(event)
	case otogi.BackpressureBlock:
		return s.enqueueBlock(ctx, event)
	default:
		return fmt.Errorf("enqueue %s: %w", s.spec.Name, otogi.ErrInvalidSubscription)
	}
}

// enqueueDropNewest drops the incoming event when the queue is full.
func (s *busSubscription) enqueueDropNewest(event *otogi.Event) error {
	select {
	case s.queue <- event:
		return nil
	default:
		return fmt.Errorf("enqueue %s: %w", s.spec.Name, otogi.ErrEventDropped)
	}
}

// enqueueDropOldest evicts one queued event before enqueueing the new event.
func (s *busSubscription) enqueueDropOldest(event *otogi.Event) error {
	select {
	case s.queue <- event:
		return nil
	default:
	}

	select {
	case <-s.queue:
	default:
	}

	select {
	case s.queue <- event:
		return nil
	default:
		return fmt.Errorf("enqueue %s: %w", s.spec.Name, otogi.ErrEventDropped)
	}
}

// enqueueBlock waits for queue capacity or caller context cancellation.
func (s *busSubscription) enqueueBlock(ctx context.Context, event *otogi.Event) error {
	select {
	case s.queue <- event:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("enqueue %s: %w", s.spec.Name, ctx.Err())
	}
}

// startWorkers launches worker goroutines and closes done after all workers exit.
func (s *busSubscription) startWorkers() {
	workerWG := &sync.WaitGroup{}
	for idx := 0; idx < s.spec.Workers; idx++ {
		workerID := idx
		workerWG.Add(1)
		go s.runWorker(workerWG, workerID)
	}

	go func() {
		workerWG.Wait()
		close(s.done)
	}()
}

// runWorker drains the queue until subscription context cancellation.
// Every handler failure is routed to the async error sink.
func (s *busSubscription) runWorker(workerWG *sync.WaitGroup, workerID int) {
	defer workerWG.Done()

	for {
		select {
		case <-s.ctx.Done():
			return
		case event := <-s.queue:
			if err := s.handleEvent(s.ctx, workerID, event); err != nil {
				s.bus.reportAsyncError(s.ctx, s.spec.Name, err)
			}
		}
	}
}

// handleEvent executes one handler call with optional timeout and panic recovery.
func (s *busSubscription) handleEvent(ctx context.Context, workerID int, event *otogi.Event) error {
	handlerCtx := ctx
	cancel := func() {}
	if s.spec.HandlerTimeout > 0 {
		handlerCtxWithTimeout, handlerCancel := context.WithTimeout(ctx, s.spec.HandlerTimeout)
		handlerCtx = handlerCtxWithTimeout
		cancel = handlerCancel
	}
	defer cancel()

	scope := fmt.Sprintf("subscription %s worker %d", s.spec.Name, workerID)
	if err := runSafely(scope, func() error {
		return s.handler(handlerCtx, event)
	}); err != nil {
		return fmt.Errorf("%s handle event %s: %w", scope, event.Kind, err)
	}

	return nil
}

// signalClose marks the subscription closed exactly once and cancels workers.
func (s *busSubscription) signalClose() {
	s.once.Do(func() {
		s.closed.Store(true)
		s.cancel()
	})
}

// shutdown waits for worker exit or returns when the supplied context expires.
func (s *busSubscription) shutdown(ctx context.Context) error {
	s.signalClose()

	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("shutdown subscription %s: %w", s.spec.Name, ctx.Err())
	}
}

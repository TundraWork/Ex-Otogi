package kernel

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"ex-otogi/pkg/otogi"
)

// moduleRecord stores module metadata and subscriptions managed by the kernel.
type moduleRecord struct {
	name          string
	module        otogi.Module
	capabilities  []otogi.Capability
	subscriptions []otogi.Subscription
	subMu         sync.Mutex
}

// addSubscription tracks subscriptions so module shutdown can close them deterministically.
func (m *moduleRecord) addSubscription(subscription otogi.Subscription) {
	m.subMu.Lock()
	defer m.subMu.Unlock()
	m.subscriptions = append(m.subscriptions, subscription)
}

// closeSubscriptions closes all tracked subscriptions and aggregates close errors.
// It clears the internal slice first to make repeated shutdown paths idempotent.
func (m *moduleRecord) closeSubscriptions(ctx context.Context) error {
	m.subMu.Lock()
	subscriptions := append([]otogi.Subscription(nil), m.subscriptions...)
	m.subscriptions = nil
	m.subMu.Unlock()

	var closeErr error
	for _, subscription := range subscriptions {
		if err := subscription.Close(ctx); err != nil {
			closeErr = errors.Join(closeErr, fmt.Errorf("close subscription %s: %w", subscription.Name(), err))
		}
	}

	return closeErr
}

// moduleRuntime is the kernel-owned implementation of otogi.ModuleRuntime.
type moduleRuntime struct {
	moduleName    string
	serviceLookup otogi.ServiceRegistry
	bus           otogi.EventBus
	record        *moduleRecord
	defaultSink   *otogi.EventSink
}

// Services returns the kernel service registry visible to the module.
func (r *moduleRuntime) Services() otogi.ServiceRegistry {
	return moduleServiceRegistry{
		base:        r.serviceLookup,
		defaultSink: cloneSinkRef(r.defaultSink),
	}
}

// Subscribe registers a module-owned subscription after capability checks.
func (r *moduleRuntime) Subscribe(
	ctx context.Context,
	interest otogi.InterestSet,
	spec otogi.SubscriptionSpec,
	handler otogi.EventHandler,
) (otogi.Subscription, error) {
	if spec.Name == "" {
		spec.Name = fmt.Sprintf("%s-subscription", r.moduleName)
	}
	if err := assertSubscriptionAllowed(r.record.capabilities, spec.Name, interest); err != nil {
		return nil, fmt.Errorf("module %s subscribe %s: %w", r.moduleName, spec.Name, err)
	}

	subscription, err := r.bus.Subscribe(ctx, interest, spec, handler)
	if err != nil {
		return nil, fmt.Errorf("module %s subscribe %s: %w", r.moduleName, spec.Name, err)
	}

	r.record.addSubscription(subscription)

	return subscription, nil
}

// assertSubscriptionAllowed enforces capability negotiation at registration time.
// A module can only subscribe to interests covered by at least one declared capability.
func assertSubscriptionAllowed(capabilities []otogi.Capability, subscriptionName string, interest otogi.InterestSet) error {
	if len(capabilities) == 0 {
		return fmt.Errorf("subscription %s requires at least one declared capability", subscriptionName)
	}

	for _, capability := range capabilities {
		if capability.Interest.Allows(interest) {
			return nil
		}
	}

	return fmt.Errorf("subscription does not match declared module capabilities")
}

type moduleServiceRegistry struct {
	base        otogi.ServiceRegistry
	defaultSink *otogi.EventSink
}

func (r moduleServiceRegistry) Register(name string, service any) error {
	if err := r.base.Register(name, service); err != nil {
		return fmt.Errorf("register service %s: %w", name, err)
	}

	return nil
}

func (r moduleServiceRegistry) Resolve(name string) (any, error) {
	service, err := r.base.Resolve(name)
	if err != nil {
		return nil, fmt.Errorf("resolve service %s: %w", name, err)
	}
	if name != otogi.ServiceSinkDispatcher {
		return service, nil
	}
	dispatcher, ok := service.(otogi.SinkDispatcher)
	if !ok {
		return nil, fmt.Errorf("resolve service %s: type assertion failed", name)
	}

	return moduleSinkDispatcher{
		base:        dispatcher,
		defaultSink: cloneSinkRef(r.defaultSink),
	}, nil
}

type moduleSinkDispatcher struct {
	base        otogi.SinkDispatcher
	defaultSink *otogi.EventSink
}

func (d moduleSinkDispatcher) SendMessage(
	ctx context.Context,
	request otogi.SendMessageRequest,
) (*otogi.OutboundMessage, error) {
	request.Target = withDefaultSink(request.Target, d.defaultSink)
	message, err := d.base.SendMessage(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("send message with module sink routing: %w", err)
	}

	return message, nil
}

func (d moduleSinkDispatcher) EditMessage(ctx context.Context, request otogi.EditMessageRequest) error {
	request.Target = withDefaultSink(request.Target, d.defaultSink)
	if err := d.base.EditMessage(ctx, request); err != nil {
		return fmt.Errorf("edit message with module sink routing: %w", err)
	}

	return nil
}

func (d moduleSinkDispatcher) DeleteMessage(ctx context.Context, request otogi.DeleteMessageRequest) error {
	request.Target = withDefaultSink(request.Target, d.defaultSink)
	if err := d.base.DeleteMessage(ctx, request); err != nil {
		return fmt.Errorf("delete message with module sink routing: %w", err)
	}

	return nil
}

func (d moduleSinkDispatcher) SetReaction(ctx context.Context, request otogi.SetReactionRequest) error {
	request.Target = withDefaultSink(request.Target, d.defaultSink)
	if err := d.base.SetReaction(ctx, request); err != nil {
		return fmt.Errorf("set reaction with module sink routing: %w", err)
	}

	return nil
}

func (d moduleSinkDispatcher) ListSinks(ctx context.Context) ([]otogi.EventSink, error) {
	sinks, err := d.base.ListSinks(ctx)
	if err != nil {
		return nil, fmt.Errorf("list sinks with module sink routing: %w", err)
	}

	return sinks, nil
}

func (d moduleSinkDispatcher) ListSinksByPlatform(
	ctx context.Context,
	platform otogi.Platform,
) ([]otogi.EventSink, error) {
	sinks, err := d.base.ListSinksByPlatform(ctx, platform)
	if err != nil {
		return nil, fmt.Errorf("list sinks by platform with module sink routing: %w", err)
	}

	return sinks, nil
}

func withDefaultSink(target otogi.OutboundTarget, defaultSink *otogi.EventSink) otogi.OutboundTarget {
	if target.Sink != nil || defaultSink == nil {
		return target
	}

	target.Sink = cloneSinkRef(defaultSink)

	return target
}

func cloneSinkRef(sink *otogi.EventSink) *otogi.EventSink {
	if sink == nil {
		return nil
	}
	cloned := *sink

	return &cloned
}

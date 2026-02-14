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
}

// Services returns the kernel service registry visible to the module.
func (r *moduleRuntime) Services() otogi.ServiceRegistry {
	return r.serviceLookup
}

// Subscribe registers a module-owned subscription after capability checks.
func (r *moduleRuntime) Subscribe(
	ctx context.Context,
	spec otogi.SubscriptionSpec,
	handler otogi.EventHandler,
) (otogi.Subscription, error) {
	if err := assertSubscriptionAllowed(r.record.capabilities, spec); err != nil {
		return nil, fmt.Errorf("module %s subscribe %s: %w", r.moduleName, spec.Name, err)
	}
	if spec.Name == "" {
		spec.Name = fmt.Sprintf("%s-subscription", r.moduleName)
	}

	subscription, err := r.bus.Subscribe(ctx, spec, handler)
	if err != nil {
		return nil, fmt.Errorf("module %s subscribe %s: %w", r.moduleName, spec.Name, err)
	}

	r.record.addSubscription(subscription)

	return subscription, nil
}

// assertSubscriptionAllowed enforces capability negotiation at registration time.
// A module can only subscribe to filters covered by at least one declared capability.
func assertSubscriptionAllowed(capabilities []otogi.Capability, spec otogi.SubscriptionSpec) error {
	if len(capabilities) == 0 {
		return nil
	}

	for _, capability := range capabilities {
		if capability.Interest.Allows(spec.Filter) {
			return nil
		}
	}

	return fmt.Errorf("subscription does not match declared module capabilities")
}

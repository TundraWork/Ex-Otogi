package kernel

import (
	"context"
	"fmt"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

type completionSubscription struct {
	channel chan otogi.EventID
	stop    func() bool
}

// SubscribeCompletion registers one bounded, cancelable completion observer for id.
// The subscription is volatile: callers must subscribe before admission and replay after acknowledgement loss.
func (k *Kernel) SubscribeCompletion(ctx context.Context, id otogi.EventID) (<-chan otogi.EventID, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if id.String() == "" {
		return nil, fmt.Errorf("subscribe completion: %w", otogi.ErrInvalid)
	}
	k.mu.Lock()
	if k.state != stateReady {
		k.mu.Unlock()
		return nil, fmt.Errorf("subscribe completion: %w", otogi.ErrInvalidState)
	}
	if len(k.completions) == k.config.queueCapacity {
		k.mu.Unlock()
		return nil, fmt.Errorf("subscribe completion: %w", otogi.ErrCapacity)
	}
	if _, exists := k.active[id]; exists {
		k.mu.Unlock()
		return nil, fmt.Errorf("subscribe completion active: %w", otogi.ErrInvalidState)
	}
	if _, exists := k.completions[id]; exists {
		k.mu.Unlock()
		return nil, fmt.Errorf("subscribe completion duplicate: %w", otogi.ErrInvalidState)
	}
	channel := make(chan otogi.EventID, 1)
	subscription := &completionSubscription{channel: channel}
	k.completions[id] = subscription
	subscription.stop = context.AfterFunc(ctx, func() { k.cancelCompletion(id, subscription) })
	k.mu.Unlock()
	return channel, nil
}

func (k *Kernel) cancelCompletion(id otogi.EventID, subscription *completionSubscription) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if current, exists := k.completions[id]; exists && current == subscription {
		delete(k.completions, id)
		close(subscription.channel)
	}
}

func (k *Kernel) complete(id otogi.EventID, acknowledge bool) {
	k.mu.Lock()
	delete(k.active, id)
	if subscription, exists := k.completions[id]; exists {
		delete(k.completions, id)
		subscription.stop()
		if acknowledge {
			subscription.channel <- id
		}
		close(subscription.channel)
	}
	k.mu.Unlock()
}

func (k *Kernel) closeCompletions() {
	k.mu.Lock()
	defer k.mu.Unlock()
	for id, subscription := range k.completions {
		delete(k.completions, id)
		subscription.stop()
		close(subscription.channel)
	}
	for id := range k.active {
		delete(k.active, id)
	}
}

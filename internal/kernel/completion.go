package kernel

import (
	"context"
	"fmt"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// TrustedAdmitter is the internal driver-facing boundary for deterministic event admission and volatile completion observation.
type TrustedAdmitter interface {
	// SubmitTrusted admits one event with a trusted deterministic runtime ID.
	SubmitTrusted(context.Context, otogi.EventID, otogi.EventInput) error
	// SubscribeCompletion observes volatile completion for one runtime event attempt.
	SubscribeCompletion(context.Context, otogi.EventID) (<-chan Completion, error)
}

// Completion acknowledges that all interested modules finished one volatile event attempt.
type Completion struct{ eventID otogi.EventID }

// EventID returns the completed runtime event identifier.
func (c Completion) EventID() otogi.EventID { return c.eventID }

type completionSubscription struct {
	channel chan Completion
	stop    func() bool
}

// SubscribeCompletion registers one bounded, cancelable completion observer for id.
// The subscription is volatile: callers must subscribe before admission and replay after acknowledgement loss.
func (k *Kernel) SubscribeCompletion(ctx context.Context, id otogi.EventID) (<-chan Completion, error) {
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
	if _, exists := k.completions[id]; exists {
		k.mu.Unlock()
		return nil, fmt.Errorf("subscribe completion duplicate: %w", otogi.ErrInvalidState)
	}
	channel := make(chan Completion, 1)
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

func (k *Kernel) complete(id otogi.EventID) {
	k.mu.Lock()
	delete(k.active, id)
	if subscription, exists := k.completions[id]; exists {
		delete(k.completions, id)
		subscription.stop()
		subscription.channel <- Completion{eventID: id}
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

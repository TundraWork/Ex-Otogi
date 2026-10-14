package kernel

import (
	"context"
	"fmt"
	"sync"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

type invocation struct {
	kernel *Kernel
	event  otogi.Event
	module otogi.ModuleID
	mu     sync.Mutex
	slots  map[string]bool
}

// Send addresses msg to the event's conversation and hands it to the messenger.
// Reusing a slot within one delivery is an error, because both sends would
// share an idempotency key and the driver would drop the second.
func (i *invocation) Send(ctx context.Context, slot string, msg otogi.Message) (otogi.EventRef, error) {
	if slot == "" || len(msg.Parts) == 0 {
		return otogi.EventRef{}, fmt.Errorf("send: empty slot or message: %w", otogi.ErrInvalid)
	}
	if err := msg.Validate(); err != nil {
		return otogi.EventRef{}, fmt.Errorf("send: %w", err)
	}
	i.mu.Lock()
	if i.slots[slot] {
		i.mu.Unlock()
		return otogi.EventRef{}, fmt.Errorf("send: slot %q already used for this event: %w", slot, otogi.ErrInvalid)
	}
	if i.slots == nil {
		i.slots = make(map[string]bool)
	}
	i.slots[slot] = true
	i.mu.Unlock()
	return i.kernel.messenger.Send(ctx, otogi.OutboundMessage{
		Platform:     i.event.Platform,
		Account:      i.event.Account,
		Conversation: i.event.Conversation.ID,
		Event:        i.event.ID,
		Module:       i.module,
		Key:          otogi.IdempotencyKeyFor(i.event.ID, i.module, slot),
		Message:      msg,
	})
}

// Package capture provides a bounded in-memory messenger for composition and tests.
package capture

import (
	"context"
	"fmt"
	"sync"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

const capacity = 16

// Messenger captures at most sixteen outbound messages without blocking.
type Messenger struct {
	mu       sync.Mutex
	messages []otogi.OutboundMessage
}

// New returns an empty capture messenger with capacity sixteen.
func New() *Messenger {
	return &Messenger{messages: make([]otogi.OutboundMessage, 0, capacity)}
}

// Send captures message unless ctx is canceled or the fixed capacity is exhausted.
func (m *Messenger) Send(ctx context.Context, message otogi.OutboundMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := otogi.NewOutboundMessage(otogi.OutboundMessageParams{
		Tenant:            message.Tenant(),
		Platform:          message.Platform(),
		Account:           message.Account(),
		Conversation:      message.Conversation(),
		OriginatingEvent:  message.OriginatingEvent(),
		OriginatingModule: message.OriginatingModule(),
		Message:           message.Message(),
		IdempotencyKey:    message.IdempotencyKey(),
	}); err != nil {
		return fmt.Errorf("capture send: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(m.messages) == capacity {
		return fmt.Errorf("capture send: %w", otogi.ErrCapacity)
	}
	m.messages = append(m.messages, message)
	return nil
}

// Messages returns an independent snapshot of captured messages in send order.
func (m *Messenger) Messages() []otogi.OutboundMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]otogi.OutboundMessage(nil), m.messages...)
}

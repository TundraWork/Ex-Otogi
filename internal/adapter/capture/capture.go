// Package capture provides an in-memory messenger that records outbound
// messages instead of delivering them, for the offline smoke and tests.
package capture

import (
	"context"
	"slices"
	"sync"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// Messenger records every message sent through it.
type Messenger struct {
	mu       sync.Mutex
	messages []otogi.OutboundMessage
}

// New returns an empty messenger.
func New() *Messenger { return &Messenger{} }

// Send records msg.
func (m *Messenger) Send(_ context.Context, msg otogi.OutboundMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, msg)
	return nil
}

// Messages returns the recorded messages in send order.
func (m *Messenger) Messages() []otogi.OutboundMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.messages)
}

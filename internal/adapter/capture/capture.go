// Package capture provides an in-memory messenger that records outbound
// messages instead of delivering them, for the offline smoke and tests.
package capture

import (
	"context"
	"slices"
	"strconv"
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

// Send records msg and returns a reference numbered by send order.
func (m *Messenger) Send(_ context.Context, msg otogi.OutboundMessage) (otogi.EventRef, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, msg)
	return otogi.EventRef{Platform: msg.Platform, Account: msg.Account, Conversation: msg.Conversation, SourceEvent: otogi.SourceEventID(strconv.Itoa(len(m.messages)))}, nil
}

// Messages returns the recorded messages in send order.
func (m *Messenger) Messages() []otogi.OutboundMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.messages)
}

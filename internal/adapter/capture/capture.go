// Package capture provides an in-memory effector that records effects
// instead of performing them, for the offline smoke and tests.
package capture

import (
	"context"
	"slices"
	"strconv"
	"sync"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// Effector records every effect applied through it.
type Effector struct {
	mu      sync.Mutex
	effects []otogi.Effect
}

// New returns an empty effector.
func New() *Effector { return &Effector{} }

// Apply records e. A send returns a reference numbered by order.
func (c *Effector) Apply(_ context.Context, e otogi.Effect) (otogi.EventRef, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.effects = append(c.effects, e)
	if e.Kind != otogi.EffectSend {
		return e.Ref, nil
	}
	return otogi.EventRef{Platform: e.To.Platform, Account: e.To.Account, Conversation: e.To.Conversation, SourceEvent: otogi.SourceEventID(strconv.Itoa(len(c.effects)))}, nil
}

// Effects returns the recorded effects in order.
func (c *Effector) Effects() []otogi.Effect {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.effects)
}

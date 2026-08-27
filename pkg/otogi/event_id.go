package otogi

import (
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"sync"
)

// EventIDGenerator generates process-scoped unique event identifiers from a random nonce and monotonic counter.
type EventIDGenerator struct{ state *eventIDGeneratorState }

type eventIDGeneratorState struct {
	mu       sync.Mutex
	nonce    [16]byte
	counter  uint64
	terminal bool
}

// NewEventIDGenerator reads exactly one 128-bit process nonce from random.
func NewEventIDGenerator(random io.Reader) (*EventIDGenerator, error) {
	return newEventIDGeneratorWithCounter(random, 0)
}

func newEventIDGeneratorWithCounter(random io.Reader, counter uint64) (*EventIDGenerator, error) {
	if random == nil {
		return nil, fmt.Errorf("event ID nonce: %w", ErrInvalid)
	}
	state := &eventIDGeneratorState{counter: counter}
	if _, err := io.ReadFull(random, state.nonce[:]); err != nil {
		return nil, fmt.Errorf("event ID nonce: %w", err)
	}
	return &EventIDGenerator{state: state}, nil
}

// Next atomically returns the next identifier and permanently fails with ErrInvalidState after counter overflow.
func (g *EventIDGenerator) Next() (EventID, error) {
	if g == nil || g.state == nil {
		return EventID{}, fmt.Errorf("event ID generator uninitialized: %w", ErrInvalidState)
	}
	s := g.state
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.terminal || s.counter == math.MaxUint64 {
		s.terminal = true
		return EventID{}, fmt.Errorf("event ID counter overflow: %w", ErrInvalidState)
	}
	s.counter++
	var encoded [52]byte
	copy(encoded[:4], "evt_")
	hex.Encode(encoded[4:36], s.nonce[:])
	const digits = "0123456789abcdef"
	counter := s.counter
	for i := len(encoded) - 1; i >= 36; i-- {
		encoded[i] = digits[counter&15]
		counter >>= 4
	}
	return EventID{value: string(encoded[:])}, nil
}

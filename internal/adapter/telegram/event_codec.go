package telegram

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// EncodeEvent serializes an event for the durable inbox.
func EncodeEvent(event otogi.Event) ([]byte, error) {
	data, err := json.Marshal(event)
	if err != nil {
		return nil, fmt.Errorf("encode inbox event: %w", err)
	}
	return data, nil
}

// DecodeEvent restores an inbox event. A row that cannot be decoded wraps
// otogi.ErrInvalid so the dispatcher fails it permanently instead of retrying.
func DecodeEvent(_ context.Context, data []byte) (otogi.Event, error) {
	var event otogi.Event
	if err := json.Unmarshal(data, &event); err != nil {
		return otogi.Event{}, fmt.Errorf("decode inbox event: %w: %w", otogi.ErrInvalid, err)
	}
	return event, nil
}

package kernel

import (
	"fmt"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

const defaultQueueCapacity = 16

// Config contains validated kernel admission settings.
type Config struct{ queueCapacity int }

// NewConfig validates queueCapacity and returns immutable kernel settings.
func NewConfig(queueCapacity int) (Config, error) {
	if queueCapacity < 1 || queueCapacity > 1024 {
		return Config{}, fmt.Errorf("kernel config: %w", otogi.ErrInvalid)
	}
	return Config{queueCapacity: queueCapacity}, nil
}

// DefaultConfig returns settings with admission capacity 16.
func DefaultConfig() Config { return Config{queueCapacity: defaultQueueCapacity} }

// QueueCapacity returns the validated admission queue capacity.
func (c Config) QueueCapacity() int { return c.queueCapacity }

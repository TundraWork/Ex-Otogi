package kernel

import (
	"fmt"
)

// runSafely executes fn and converts panics into returned errors tagged with scope.
// It is used at goroutine and lifecycle boundaries to prevent process-wide crashes.
func runSafely(scope string, fn func() error) (err error) {
	defer func() {
		recovered := recover()
		if recovered == nil {
			return
		}
		err = fmt.Errorf("%s: panic recovered: %v", scope, recovered)
	}()

	if err := fn(); err != nil {
		return fmt.Errorf("%s: %w", scope, err)
	}

	return nil
}

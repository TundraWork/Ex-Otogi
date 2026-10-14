package otogi

import "errors"

var (
	// ErrInvalid reports a value that violates a contract, such as an event
	// that fails Validate.
	ErrInvalid = errors.New("invalid")
	// ErrUnavailable reports a component that cannot take work right now,
	// for example a kernel that is not started or is draining.
	ErrUnavailable = errors.New("unavailable")
	// ErrUnsupported reports valid content that a driver cannot represent on
	// its platform.
	ErrUnsupported = errors.New("unsupported")
)

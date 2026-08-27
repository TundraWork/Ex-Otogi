package otogi

import "errors"

var (
	// ErrInvalid identifies input that violates a contract.
	ErrInvalid = errors.New("invalid")
	// ErrUnavailable identifies a dependency that cannot currently serve work.
	ErrUnavailable = errors.New("unavailable")
	// ErrCapacity identifies work rejected because bounded capacity is exhausted.
	ErrCapacity = errors.New("capacity exceeded")
	// ErrInvalidState identifies an operation forbidden by the current lifecycle state.
	ErrInvalidState = errors.New("invalid state")
	// ErrModulePanic identifies a module invocation that panicked.
	ErrModulePanic = errors.New("module panic")
	// ErrUnsupported identifies valid content that an adapter cannot represent.
	ErrUnsupported = errors.New("unsupported")
)

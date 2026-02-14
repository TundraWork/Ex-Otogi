package otogi

import "errors"

var (
	// ErrInvalidEvent indicates that an event does not satisfy protocol invariants.
	ErrInvalidEvent = errors.New("otogi: invalid event")
	// ErrInvalidOutboundRequest indicates malformed outbound dispatcher requests.
	ErrInvalidOutboundRequest = errors.New("otogi: invalid outbound request")
	// ErrInvalidSubscription indicates that a subscription configuration is invalid.
	ErrInvalidSubscription = errors.New("otogi: invalid subscription")
	// ErrSubscriptionClosed indicates that a subscription is no longer active.
	ErrSubscriptionClosed = errors.New("otogi: subscription closed")
	// ErrEventDropped indicates a non-blocking backpressure drop.
	ErrEventDropped = errors.New("otogi: event dropped due to backpressure")
	// ErrOutboundUnsupported indicates that a platform cannot satisfy the requested outbound action.
	ErrOutboundUnsupported = errors.New("otogi: outbound operation unsupported")
	// ErrServiceAlreadyRegistered indicates duplicate service registration.
	ErrServiceAlreadyRegistered = errors.New("otogi: service already registered")
	// ErrServiceNotFound indicates a service lookup miss.
	ErrServiceNotFound = errors.New("otogi: service not found")
	// ErrModuleAlreadyRegistered indicates duplicate module registration.
	ErrModuleAlreadyRegistered = errors.New("otogi: module already registered")
	// ErrDriverAlreadyRegistered indicates duplicate driver registration.
	ErrDriverAlreadyRegistered = errors.New("otogi: driver already registered")
)

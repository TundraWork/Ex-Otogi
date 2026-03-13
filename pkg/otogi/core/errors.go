package core

import "errors"

var (
	// ErrInvalidSubscription indicates that a subscription configuration is invalid.
	ErrInvalidSubscription = errors.New("otogi: invalid subscription")
	// ErrSubscriptionClosed indicates that a subscription is no longer active.
	ErrSubscriptionClosed = errors.New("otogi: subscription closed")
	// ErrEventDropped indicates a non-blocking backpressure drop.
	ErrEventDropped = errors.New("otogi: event dropped due to backpressure")
	// ErrServiceAlreadyRegistered indicates duplicate service registration.
	ErrServiceAlreadyRegistered = errors.New("otogi: service already registered")
	// ErrServiceNotFound indicates a service lookup miss.
	ErrServiceNotFound = errors.New("otogi: service not found")
	// ErrModuleAlreadyRegistered indicates duplicate module registration.
	ErrModuleAlreadyRegistered = errors.New("otogi: module already registered")
	// ErrDriverAlreadyRegistered indicates duplicate driver registration.
	ErrDriverAlreadyRegistered = errors.New("otogi: driver already registered")
	// ErrConfigNotFound indicates a module config lookup miss.
	ErrConfigNotFound = errors.New("otogi: module config not found")
	// ErrConfigAlreadyRegistered indicates duplicate module config registration.
	ErrConfigAlreadyRegistered = errors.New("otogi: module config already registered")
)

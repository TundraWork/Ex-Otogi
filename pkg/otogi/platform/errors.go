package platform

import "errors"

var (
	// ErrInvalidEvent indicates that an event does not satisfy protocol invariants.
	ErrInvalidEvent = errors.New("otogi: invalid event")
	// ErrInvalidMediaDownloadRequest indicates malformed media download requests.
	ErrInvalidMediaDownloadRequest = errors.New("otogi: invalid media download request")
	// ErrMediaDownloadNotFound indicates requested media could not be resolved.
	ErrMediaDownloadNotFound = errors.New("otogi: media download target not found")
	// ErrMediaDownloadUnsupported indicates a platform cannot satisfy the requested media download.
	ErrMediaDownloadUnsupported = errors.New("otogi: media download unsupported")
	// ErrInvalidOutboundRequest indicates malformed outbound dispatcher requests.
	ErrInvalidOutboundRequest = errors.New("otogi: invalid outbound request")
	// ErrOutboundUnsupported indicates that a platform cannot satisfy the requested outbound action.
	ErrOutboundUnsupported = errors.New("otogi: outbound operation unsupported")
)

package management

import "errors"

var (
	// ErrEventNotFound indicates that the requested event is not retained in the
	// current process window.
	ErrEventNotFound = errors.New("otogi: management event not found")
	// ErrArtifactNotFound indicates that the requested artifact is not retained
	// in the current process window.
	ErrArtifactNotFound = errors.New("otogi: management artifact not found")
	// ErrSnapshotNotFound indicates that the requested snapshot is not retained.
	ErrSnapshotNotFound = errors.New("otogi: management snapshot not found")
	// ErrInvalidQuery indicates that a management query request is malformed.
	ErrInvalidQuery = errors.New("otogi: management invalid query")
)

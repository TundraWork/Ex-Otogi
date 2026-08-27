package otogi

import "fmt"

// ResultStatus identifies the sole terminal disposition of a module invocation.
type ResultStatus string

const (
	// ResultSucceeded indicates successful handling.
	ResultSucceeded ResultStatus = "succeeded"
	// ResultRejected indicates intentional permanent rejection without an error.
	ResultRejected ResultStatus = "rejected"
	// ResultFailed indicates handling failed with an error.
	ResultFailed ResultStatus = "failed"
	// ResultCanceled indicates handling ended due to cancellation with an error.
	ResultCanceled ResultStatus = "canceled"
)

// Result is an immutable terminal module invocation disposition.
type Result struct {
	status ResultStatus
	err    error
}

// NewResult validates that failed and canceled results have errors and other results do not.
func NewResult(status ResultStatus, err error) (Result, error) {
	needsError := status == ResultFailed || status == ResultCanceled
	validStatus := status == ResultSucceeded || status == ResultRejected || needsError
	if !validStatus || needsError != (err != nil) {
		return Result{}, fmt.Errorf("result: %w", ErrInvalid)
	}
	return Result{status: status, err: err}, nil
}

// NewSucceededResult returns a succeeded result without an error.
func NewSucceededResult() Result { return Result{status: ResultSucceeded} }

// NewRejectedResult returns a rejected result without an error.
func NewRejectedResult() Result { return Result{status: ResultRejected} }

// NewFailedResult returns a failed result preserving err; nil is represented by ErrInvalid to preserve the invariant.
func NewFailedResult(err error) Result {
	if err == nil {
		err = ErrInvalid
	}
	return Result{status: ResultFailed, err: err}
}

// NewCanceledResult returns a canceled result preserving err; nil is represented by ErrInvalid to preserve the invariant.
func NewCanceledResult(err error) Result {
	if err == nil {
		err = ErrInvalid
	}
	return Result{status: ResultCanceled, err: err}
}

// Status returns the result's sole terminal status.
func (r Result) Status() ResultStatus { return r.status }

// Err returns the preserved failure or cancellation error, or nil for success and rejection.
func (r Result) Err() error { return r.err }

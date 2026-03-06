package otogi

import (
	"errors"
	"strings"
	"testing"
)

func TestNewInvalidEventDetailErrorUnwrapsSentinelAndCause(t *testing.T) {
	t.Parallel()

	cause := errors.New("bad entities")
	err := newInvalidEventDetailError("article.created invalid entities", cause)

	if !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("error = %v, want ErrInvalidEvent", err)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("error = %v, want wrapped cause", err)
	}
	if !strings.Contains(err.Error(), "article.created invalid entities") {
		t.Fatalf("error = %q, want detail in message", err.Error())
	}
}

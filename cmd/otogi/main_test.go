package main

import (
	"bytes"
	"context"
	"testing"
)

func TestRunComposesScriptedEchoBridge(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer

	if err := run(context.Background(), &output); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got, want := output.String(), "echo: hello\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

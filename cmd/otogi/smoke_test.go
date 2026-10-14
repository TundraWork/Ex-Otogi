package main

import (
	"bytes"
	"context"
	"testing"
)

func TestSmokeEchoesHello(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer

	if err := smoke(context.Background(), &output); err != nil {
		t.Fatalf("smoke: %v", err)
	}
	if got, want := output.String(), "echo: hello\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

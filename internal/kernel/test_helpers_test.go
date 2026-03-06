package kernel

import "testing"

func newTestKernel(t *testing.T, options ...Option) *Kernel {
	t.Helper()

	kernelRuntime, err := New(options...)
	if err != nil {
		t.Fatalf("new kernel failed: %v", err)
	}

	return kernelRuntime
}

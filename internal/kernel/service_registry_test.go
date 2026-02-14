package kernel

import (
	"errors"
	"testing"

	"ex-otogi/pkg/otogi"
)

// TestServiceRegistryRegisterAndResolve verifies happy-path registration and lookup.
func TestServiceRegistryRegisterAndResolve(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		registerName  string
		registerValue any
		resolveName   string
		wantResolve   any
		wantErr       error
	}{
		{
			name:          "register and resolve success",
			registerName:  "cache",
			registerValue: "redis",
			resolveName:   "cache",
			wantResolve:   "redis",
		},
		{
			name:          "duplicate registration fails",
			registerName:  "db",
			registerValue: "postgres",
			resolveName:   "db",
			wantResolve:   "postgres",
			wantErr:       otogi.ErrServiceAlreadyRegistered,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			registry := NewServiceRegistry()
			if err := registry.Register(testCase.registerName, testCase.registerValue); err != nil {
				t.Fatalf("first register failed: %v", err)
			}

			if testCase.wantErr != nil {
				err := registry.Register(testCase.registerName, "duplicate")
				if !errors.Is(err, testCase.wantErr) {
					t.Fatalf("duplicate register error = %v, want %v", err, testCase.wantErr)
				}
			}

			resolved, err := registry.Resolve(testCase.resolveName)
			if err != nil {
				t.Fatalf("resolve failed: %v", err)
			}
			if resolved != testCase.wantResolve {
				t.Fatalf("resolve value = %v, want %v", resolved, testCase.wantResolve)
			}
		})
	}
}

// TestServiceRegistryErrors verifies validation and not-found failure semantics.
func TestServiceRegistryErrors(t *testing.T) {
	t.Parallel()

	registry := NewServiceRegistry()

	if err := registry.Register("", "value"); err == nil {
		t.Fatal("expected empty name register error")
	}
	if err := registry.Register("svc", nil); err == nil {
		t.Fatal("expected nil service register error")
	}
	var nilPointerService *struct{}
	if err := registry.Register("svc-pointer", nilPointerService); err == nil {
		t.Fatal("expected nil pointer service register error")
	}
	if _, err := registry.Resolve("missing"); !errors.Is(err, otogi.ErrServiceNotFound) {
		t.Fatalf("resolve missing error = %v, want %v", err, otogi.ErrServiceNotFound)
	}
}

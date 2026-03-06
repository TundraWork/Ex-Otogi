package kernel

import (
	"encoding/json"
	"errors"
	"testing"

	"ex-otogi/pkg/otogi"
)

func TestConfigRegistryRegisterAndResolveCopiesConfig(t *testing.T) {
	t.Parallel()

	registry := NewConfigRegistry()
	raw := json.RawMessage(`{"signing_key":"value"}`)

	if err := registry.Register("sleep", raw); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	raw[2] = 'X'

	resolved, err := registry.Resolve("sleep")
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if string(resolved) != `{"signing_key":"value"}` {
		t.Fatalf("resolved config = %s, want original copy", resolved)
	}

	resolved[2] = 'Y'

	resolvedAgain, err := registry.Resolve("sleep")
	if err != nil {
		t.Fatalf("Resolve after mutation failed: %v", err)
	}
	if string(resolvedAgain) != `{"signing_key":"value"}` {
		t.Fatalf("resolved config after mutation = %s, want original copy", resolvedAgain)
	}
}

func TestConfigRegistryErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		run     func(*ConfigRegistry) error
		wantErr error
	}{
		{
			name: "register duplicate module",
			run: func(registry *ConfigRegistry) error {
				raw := json.RawMessage(`{"value":"first"}`)
				if err := registry.Register("sleep", raw); err != nil {
					return err
				}

				return registry.Register("sleep", raw)
			},
			wantErr: otogi.ErrConfigAlreadyRegistered,
		},
		{
			name: "resolve missing module",
			run: func(registry *ConfigRegistry) error {
				_, err := registry.Resolve("missing")
				return err
			},
			wantErr: otogi.ErrConfigNotFound,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			registry := NewConfigRegistry()
			err := testCase.run(registry)
			if err == nil {
				t.Fatal("expected error")
			}
			if !errors.Is(err, testCase.wantErr) {
				t.Fatalf("error = %v, want %v", err, testCase.wantErr)
			}
		})
	}
}

func TestConfigRegistryRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(*ConfigRegistry) error
	}{
		{
			name: "register empty module name",
			run: func(registry *ConfigRegistry) error {
				return registry.Register("", json.RawMessage(`{"value":"x"}`))
			},
		},
		{
			name: "register empty config",
			run: func(registry *ConfigRegistry) error {
				return registry.Register("sleep", nil)
			},
		},
		{
			name: "resolve empty module name",
			run: func(registry *ConfigRegistry) error {
				_, err := registry.Resolve("")
				return err
			},
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			registry := NewConfigRegistry()
			if err := testCase.run(registry); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

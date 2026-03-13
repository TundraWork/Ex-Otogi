package sleep

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"testing"

	"ex-otogi/pkg/otogi/core"
)

func testSigningKey() []byte {
	return []byte("0123456789abcdefghijklmnopqrstuv")
}

func testConfigRegistry(t *testing.T) core.ConfigRegistry {
	t.Helper()

	encoded := base64.RawURLEncoding.EncodeToString(testSigningKey())
	raw, err := json.Marshal(fileConfig{SigningKey: encoded})
	if err != nil {
		t.Fatalf("marshal test config: %v", err)
	}

	registry := newConfigRegistryStub()
	if err := registry.Register("sleep", raw); err != nil {
		t.Fatalf("register test config: %v", err)
	}

	return registry
}

type configRegistryStub struct {
	configs map[string]json.RawMessage
}

func newConfigRegistryStub() *configRegistryStub {
	return &configRegistryStub{
		configs: make(map[string]json.RawMessage),
	}
}

func (r *configRegistryStub) Register(moduleName string, raw json.RawMessage) error {
	if moduleName == "" {
		return fmt.Errorf("register module config: empty module name")
	}
	if len(raw) == 0 {
		return fmt.Errorf("register module config %s: empty config", moduleName)
	}
	if _, exists := r.configs[moduleName]; exists {
		return fmt.Errorf("register module config %s: %w", moduleName, core.ErrConfigAlreadyRegistered)
	}

	r.configs[moduleName] = append(json.RawMessage(nil), raw...)

	return nil
}

func (r *configRegistryStub) Resolve(moduleName string) (json.RawMessage, error) {
	raw, exists := r.configs[moduleName]
	if !exists {
		return nil, fmt.Errorf("resolve module config %s: %w", moduleName, core.ErrConfigNotFound)
	}

	return append(json.RawMessage(nil), raw...), nil
}

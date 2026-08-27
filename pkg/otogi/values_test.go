package otogi

import (
	"errors"
	"strings"
	"testing"
)

func TestIdentifierContracts(t *testing.T) {
	tests := []struct {
		name string
		new  func(string) (string, error)
	}{
		{"event", func(s string) (string, error) { v, e := NewEventID(s); return v.String(), e }},
		{"source event", func(s string) (string, error) { v, e := NewSourceEventID(s); return v.String(), e }},
		{"trace", func(s string) (string, error) { v, e := NewTraceID(s); return v.String(), e }},
		{"causal event", func(s string) (string, error) { v, e := NewCausalEventID(s); return v.String(), e }},
		{"platform", func(s string) (string, error) { v, e := NewPlatformID(s); return v.String(), e }},
		{"account", func(s string) (string, error) { v, e := NewAccountID(s); return v.String(), e }},
		{"tenant", func(s string) (string, error) { v, e := NewTenantID(s); return v.String(), e }},
		{"conversation", func(s string) (string, error) { v, e := NewConversationID(s); return v.String(), e }},
		{"actor", func(s string) (string, error) { v, e := NewActorID(s); return v.String(), e }},
		{"module", func(s string) (string, error) { v, e := NewModuleID(s); return v.String(), e }},
		{"idempotency", func(s string) (string, error) { v, e := NewIdempotencyKey(s); return v.String(), e }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, err := tt.new("alpha"); err != nil || got != "alpha" {
				t.Fatalf("valid: got %q, %v", got, err)
			}
			for _, invalid := range []string{"", " alpha", "alpha ", strings.Repeat("a", 129), string([]byte{0xff})} {
				if _, err := tt.new(invalid); !errors.Is(err, ErrInvalid) {
					t.Fatalf("%q: got %v, want ErrInvalid", invalid, err)
				}
			}
		})
	}
}

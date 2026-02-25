package llm

import (
	"context"
	"strings"
	"testing"

	"ex-otogi/pkg/otogi"
)

func TestRegistryResolve(t *testing.T) {
	t.Parallel()

	provider := &providerStub{}
	registry, err := NewRegistry(map[string]otogi.LLMProvider{
		"openai-main": provider,
	})
	if err != nil {
		t.Fatalf("NewRegistry failed: %v", err)
	}

	tests := []struct {
		name             string
		key              string
		wantErrSubstring string
		wantSameProvider bool
	}{
		{
			name:             "known provider",
			key:              "openai-main",
			wantSameProvider: true,
		},
		{
			name:             "unknown provider",
			key:              "missing",
			wantErrSubstring: "is not configured",
		},
		{
			name:             "empty provider key",
			key:              "   ",
			wantErrSubstring: "empty provider key",
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			resolved, err := registry.Resolve(testCase.key)
			if testCase.wantErrSubstring != "" {
				if err == nil {
					t.Fatal("expected error")
				}
				if !strings.Contains(err.Error(), testCase.wantErrSubstring) {
					t.Fatalf("error = %v, want substring %q", err, testCase.wantErrSubstring)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve failed: %v", err)
			}
			if testCase.wantSameProvider && resolved != provider {
				t.Fatal("resolved provider pointer mismatch")
			}
		})
	}
}

type providerStub struct{}

func (providerStub) GenerateStream(context.Context, otogi.LLMGenerateRequest) (otogi.LLMStream, error) {
	return nil, nil
}

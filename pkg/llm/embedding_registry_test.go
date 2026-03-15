package llm

import (
	"context"
	"strings"
	"testing"

	"ex-otogi/pkg/otogi/ai"
)

func TestEmbeddingRegistryResolve(t *testing.T) {
	t.Parallel()

	provider := &embeddingProviderStub{}
	registry, err := NewEmbeddingRegistry(map[string]ai.EmbeddingProvider{
		"gemini-main": provider,
	})
	if err != nil {
		t.Fatalf("NewEmbeddingRegistry failed: %v", err)
	}

	tests := []struct {
		name             string
		key              string
		wantErrSubstring string
		wantSameProvider bool
	}{
		{
			name:             "known provider",
			key:              "gemini-main",
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

type embeddingProviderStub struct{}

func (embeddingProviderStub) Embed(context.Context, ai.EmbeddingRequest) (ai.EmbeddingResponse, error) {
	return ai.EmbeddingResponse{}, nil
}

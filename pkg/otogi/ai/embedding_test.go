package ai

import "testing"

func TestEmbeddingRequestValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		req     EmbeddingRequest
		wantErr bool
	}{
		{
			name: "valid single text",
			req: EmbeddingRequest{
				Model:    "text-embedding-3-small",
				Texts:    []string{"hello"},
				TaskType: EmbeddingTaskTypeQuery,
			},
		},
		{
			name: "valid multiple texts",
			req: EmbeddingRequest{
				Model:      "gemini-embedding-001",
				Texts:      []string{"hello", "world"},
				Dimensions: 512,
				TaskType:   EmbeddingTaskTypeDocument,
			},
		},
		{
			name: "empty model",
			req: EmbeddingRequest{
				Texts: []string{"hello"},
			},
			wantErr: true,
		},
		{
			name: "empty texts slice",
			req: EmbeddingRequest{
				Model: "model",
			},
			wantErr: true,
		},
		{
			name: "empty text element",
			req: EmbeddingRequest{
				Model: "model",
				Texts: []string{"hello", "   "},
			},
			wantErr: true,
		},
		{
			name: "negative dimensions",
			req: EmbeddingRequest{
				Model:      "model",
				Texts:      []string{"hello"},
				Dimensions: -1,
			},
			wantErr: true,
		},
		{
			name: "invalid task type",
			req: EmbeddingRequest{
				Model:    "model",
				Texts:    []string{"hello"},
				TaskType: "semantic",
			},
			wantErr: true,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			err := testCase.req.Validate()
			if testCase.wantErr && err == nil {
				t.Fatal("expected error")
			}
			if !testCase.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

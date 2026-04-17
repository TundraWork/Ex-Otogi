package ai

import (
	"strings"
	"testing"
	"time"
)

func TestSemanticLinkValidate(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		link    SemanticLink
		wantErr string
	}{
		{
			name:    "valid related",
			link:    SemanticLink{TargetID: "mem-1", Relation: "related"},
			wantErr: "",
		},
		{
			name:    "valid refines",
			link:    SemanticLink{TargetID: "mem-1", Relation: "refines"},
			wantErr: "",
		},
		{
			name:    "valid supersedes",
			link:    SemanticLink{TargetID: "mem-1", Relation: "supersedes"},
			wantErr: "",
		},
		{
			name:    "empty relation is valid",
			link:    SemanticLink{TargetID: "mem-1", Relation: ""},
			wantErr: "",
		},
		{
			name:    "missing target_id",
			link:    SemanticLink{TargetID: "", Relation: "related"},
			wantErr: "missing target_id",
		},
		{
			name:    "unsupported relation",
			link:    SemanticLink{TargetID: "mem-1", Relation: "unknown"},
			wantErr: "unsupported relation",
		},
	}

	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			err := testCase.link.Validate()
			if testCase.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate failed: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate error = nil, want %q", testCase.wantErr)
			}
			if !strings.Contains(err.Error(), testCase.wantErr) {
				t.Fatalf("Validate error = %q, want substring %q", err, testCase.wantErr)
			}
		})
	}
}

func TestSemanticUpdateValidate(t *testing.T) {
	t.Parallel()

	valid := SemanticUpdate{
		ID:        "mem-1",
		Content:   "Alice likes tea",
		Category:  "preference",
		Embedding: []float32{1, 0},
		Profile: SemanticProfile{
			Kind:           SemanticKindUnit,
			Importance:     7,
			LastAccessedAt: time.Unix(100, 0).UTC(),
			AccessCount:    2,
			Source:         "natural",
			SourceActor: &SemanticActorRef{
				ID:   "user-1",
				Name: "Alice",
			},
		},
	}

	testCases := []struct {
		name    string
		mutate  func(*SemanticUpdate)
		wantErr string
	}{
		{
			name:   "valid",
			mutate: func(*SemanticUpdate) {},
		},
		{
			name: "missing id",
			mutate: func(update *SemanticUpdate) {
				update.ID = ""
			},
			wantErr: "missing id",
		},
		{
			name: "invalid kind",
			mutate: func(update *SemanticUpdate) {
				update.Profile.Kind = "mystery"
			},
			wantErr: "unsupported kind",
		},
		{
			name: "negative access count",
			mutate: func(update *SemanticUpdate) {
				update.Profile.AccessCount = -1
			},
			wantErr: "access_count must be >= 0",
		},
	}

	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			update := valid
			update.Profile = valid.Profile
			testCase.mutate(&update)

			err := update.Validate()
			if testCase.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate failed: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate error = nil, want %q", testCase.wantErr)
			}
			if !strings.Contains(err.Error(), testCase.wantErr) {
				t.Fatalf("Validate error = %q, want substring %q", err, testCase.wantErr)
			}
		})
	}
}

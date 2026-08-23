package ai

import "testing"

func TestSemanticRetrievalPolicyValidateAcceptsZeroValues(t *testing.T) {
	t.Parallel()

	if err := (SemanticRetrievalPolicy{}).Validate(); err != nil {
		t.Fatalf("zero-value policy should be valid: %v", err)
	}
}

func TestSemanticRetrievalPolicyValidateRejectsNegativeMaxRetrievedMemories(t *testing.T) {
	t.Parallel()

	policy := SemanticRetrievalPolicy{MaxRetrievedMemories: -1}
	if err := policy.Validate(); err == nil {
		t.Fatal("expected error for negative max_retrieved_memories")
	}
}

func TestSemanticRetrievalPolicyValidateRejectsInvalidMinSimilarity(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name  string
		value float32
	}{
		{"negative", -0.1},
		{"above_one", 1.1},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			policy := SemanticRetrievalPolicy{MinSimilarity: testCase.value}
			if err := policy.Validate(); err == nil {
				t.Fatalf("expected error for min_similarity %f", testCase.value)
			}
		})
	}
}

func TestSemanticRetrievalPolicyValidateRejectsNegativeMaxMemoryRunes(t *testing.T) {
	t.Parallel()

	policy := SemanticRetrievalPolicy{MaxMemoryRunes: -1}
	if err := policy.Validate(); err == nil {
		t.Fatal("expected error for negative max_memory_runes")
	}
}

func TestSemanticRetrievalRequestValidateHappyPath(t *testing.T) {
	t.Parallel()

	req := SemanticRetrievalRequest{
		Scope:        SemanticScope{Platform: "test", ConversationID: "c1"},
		Prompt:       "hello",
		Policy:       SemanticRetrievalPolicy{MaxRetrievedMemories: 5},
		CurrentActor: SemanticActorRef{ID: "a1"},
	}
	if err := req.Validate(); err != nil {
		t.Fatalf("valid request should pass: %v", err)
	}
}

func TestSemanticRetrievalRequestValidateRejectsMissingScope(t *testing.T) {
	t.Parallel()

	req := SemanticRetrievalRequest{
		Prompt: "hello",
	}
	if err := req.Validate(); err == nil {
		t.Fatal("expected error for missing scope fields")
	}
}

func TestSemanticRetrievalRequestValidateRejectsMissingPrompt(t *testing.T) {
	t.Parallel()

	req := SemanticRetrievalRequest{
		Scope:  SemanticScope{Platform: "test", ConversationID: "c1"},
		Prompt: "",
	}
	if err := req.Validate(); err == nil {
		t.Fatal("expected error for missing prompt")
	}
}

func TestSemanticRetrievalRequestValidateRejectsBadPolicy(t *testing.T) {
	t.Parallel()

	req := SemanticRetrievalRequest{
		Scope:  SemanticScope{Platform: "test", ConversationID: "c1"},
		Prompt: "hello",
		Policy: SemanticRetrievalPolicy{MinSimilarity: -1},
	}
	if err := req.Validate(); err == nil {
		t.Fatal("expected error for bad policy")
	}
}

func TestSemanticRetrievalRequestValidateRejectsBadRelatedActor(t *testing.T) {
	t.Parallel()

	req := SemanticRetrievalRequest{
		Scope:         SemanticScope{Platform: "test", ConversationID: "c1"},
		Prompt:        "hello",
		RelatedActors: []SemanticActorRef{{ID: "", Name: ""}},
	}
	if err := req.Validate(); err == nil {
		t.Fatal("expected error for invalid related actor")
	}
}

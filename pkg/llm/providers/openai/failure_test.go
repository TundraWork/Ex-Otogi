package openai

import (
	"errors"
	"testing"

	"ex-otogi/pkg/otogi/ai"
)

func TestOpenAIStreamFailureClassification(t *testing.T) {
	tests := []struct {
		code      string
		wantClass ai.LLMFailureClass
		wantRetry bool
	}{
		{code: "rate_limit_exceeded", wantClass: ai.LLMFailureRateLimited, wantRetry: true},
		{code: "server_error", wantClass: ai.LLMFailureProviderUnavailable, wantRetry: true},
		{code: "invalid_prompt", wantClass: ai.LLMFailureInvalidRequest},
		{code: "image_content_policy_violation", wantClass: ai.LLMFailureSafetyRefusal},
	}
	for _, test := range tests {
		err := classifyOpenAIStreamCode(test.code, errors.New("provider failure"))
		if got := ai.ClassifyLLMFailure(err); got != test.wantClass {
			t.Fatalf("code %s class = %s, want %s", test.code, got, test.wantClass)
		}
		_, retry := ai.LLMFailureRetry(err)
		if retry != test.wantRetry {
			t.Fatalf("code %s retry = %t, want %t", test.code, retry, test.wantRetry)
		}
	}
}

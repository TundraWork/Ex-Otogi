package gemini

import (
	"testing"

	"ex-otogi/pkg/otogi/ai"

	"google.golang.org/genai"
)

func TestGeminiAPIFailureClassification(t *testing.T) {
	tests := []struct {
		err       genai.APIError
		wantClass ai.LLMFailureClass
		wantRetry bool
	}{
		{err: genai.APIError{Code: 401}, wantClass: ai.LLMFailureAuthentication},
		{err: genai.APIError{Code: 429}, wantClass: ai.LLMFailureRateLimited, wantRetry: true},
		{err: genai.APIError{Code: 503}, wantClass: ai.LLMFailureProviderUnavailable, wantRetry: true},
		{err: genai.APIError{Code: 400}, wantClass: ai.LLMFailureInvalidRequest},
	}
	for _, test := range tests {
		class, retry := geminiAPIFailure(test.err)
		if class != test.wantClass || retry != test.wantRetry {
			t.Fatalf("API error %#v = (%s,%t), want (%s,%t)", test.err, class, retry, test.wantClass, test.wantRetry)
		}
	}
}

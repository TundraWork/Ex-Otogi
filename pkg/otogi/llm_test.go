package otogi

import "testing"

func TestLLMMessageRoleValidate(t *testing.T) {
	tests := []struct {
		name    string
		role    LLMMessageRole
		wantErr bool
	}{
		{name: "system role", role: LLMMessageRoleSystem},
		{name: "user role", role: LLMMessageRoleUser},
		{name: "assistant role", role: LLMMessageRoleAssistant},
		{name: "invalid role", role: "tool", wantErr: true},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			err := testCase.role.Validate()
			if testCase.wantErr && err == nil {
				t.Fatal("expected error")
			}
			if !testCase.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestLLMMessageValidate(t *testing.T) {
	tests := []struct {
		name    string
		message LLMMessage
		wantErr bool
	}{
		{
			name: "valid message",
			message: LLMMessage{
				Role:    LLMMessageRoleUser,
				Content: "hello",
			},
		},
		{
			name: "invalid role",
			message: LLMMessage{
				Role:    "tool",
				Content: "hello",
			},
			wantErr: true,
		},
		{
			name: "empty content",
			message: LLMMessage{
				Role:    LLMMessageRoleAssistant,
				Content: "   ",
			},
			wantErr: true,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			err := testCase.message.Validate()
			if testCase.wantErr && err == nil {
				t.Fatal("expected error")
			}
			if !testCase.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestLLMGenerateRequestValidate(t *testing.T) {
	tests := []struct {
		name    string
		req     LLMGenerateRequest
		wantErr bool
	}{
		{
			name: "valid request",
			req: LLMGenerateRequest{
				Model: "gpt-5-mini",
				Messages: []LLMMessage{
					{Role: LLMMessageRoleSystem, Content: "You are a bot."},
					{Role: LLMMessageRoleUser, Content: "Hello"},
				},
				MaxOutputTokens: 256,
				Temperature:     0.7,
				Metadata: map[string]string{
					"agent": "otogi",
				},
			},
		},
		{
			name: "missing model",
			req: LLMGenerateRequest{
				Messages: []LLMMessage{{Role: LLMMessageRoleUser, Content: "hello"}},
			},
			wantErr: true,
		},
		{
			name: "missing messages",
			req: LLMGenerateRequest{
				Model: "gpt-5-mini",
			},
			wantErr: true,
		},
		{
			name: "invalid message",
			req: LLMGenerateRequest{
				Model: "gpt-5-mini",
				Messages: []LLMMessage{
					{Role: "tool", Content: "hello"},
				},
			},
			wantErr: true,
		},
		{
			name: "negative max output tokens",
			req: LLMGenerateRequest{
				Model: "gpt-5-mini",
				Messages: []LLMMessage{
					{Role: LLMMessageRoleUser, Content: "hello"},
				},
				MaxOutputTokens: -1,
			},
			wantErr: true,
		},
		{
			name: "negative temperature",
			req: LLMGenerateRequest{
				Model: "gpt-5-mini",
				Messages: []LLMMessage{
					{Role: LLMMessageRoleUser, Content: "hello"},
				},
				Temperature: -0.1,
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

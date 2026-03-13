package ai

import "testing"

func TestLLMGenerateChunkKindNormalize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		kind LLMGenerateChunkKind
		want LLMGenerateChunkKind
	}{
		{
			name: "empty kind defaults to output text",
			kind: "",
			want: LLMGenerateChunkKindOutputText,
		},
		{
			name: "output text remains output text",
			kind: LLMGenerateChunkKindOutputText,
			want: LLMGenerateChunkKindOutputText,
		},
		{
			name: "thinking summary remains thinking summary",
			kind: LLMGenerateChunkKindThinkingSummary,
			want: LLMGenerateChunkKindThinkingSummary,
		},
		{
			name: "unknown kind defaults to output text",
			kind: "custom",
			want: LLMGenerateChunkKindOutputText,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			if got := testCase.kind.Normalize(); got != testCase.want {
				t.Fatalf("Normalize() = %q, want %q", got, testCase.want)
			}
		})
	}
}

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
		{
			name: "valid multimodal message",
			message: LLMMessage{
				Role: LLMMessageRoleUser,
				Parts: []LLMMessagePart{
					{Type: LLMMessagePartTypeText, Text: "describe this image"},
					{
						Type: LLMMessagePartTypeImage,
						Image: &LLMInputImage{
							MIMEType: "image/jpeg",
							Data:     []byte{1, 2, 3},
							Detail:   LLMInputImageDetailHigh,
						},
					},
				},
			},
		},
		{
			name: "content and parts conflict",
			message: LLMMessage{
				Role:    LLMMessageRoleUser,
				Content: "hello",
				Parts: []LLMMessagePart{
					{Type: LLMMessagePartTypeText, Text: "world"},
				},
			},
			wantErr: true,
		},
		{
			name: "invalid image part",
			message: LLMMessage{
				Role: LLMMessageRoleUser,
				Parts: []LLMMessagePart{
					{
						Type:  LLMMessagePartTypeImage,
						Image: &LLMInputImage{},
					},
				},
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

func TestLLMMessageContentParts(t *testing.T) {
	t.Parallel()

	t.Run("content is promoted to one text part", func(t *testing.T) {
		t.Parallel()

		message := LLMMessage{
			Role:    LLMMessageRoleUser,
			Content: "hello",
		}
		parts := message.ContentParts()
		if len(parts) != 1 {
			t.Fatalf("parts len = %d, want 1", len(parts))
		}
		if parts[0].Type != LLMMessagePartTypeText || parts[0].Text != "hello" {
			t.Fatalf("parts[0] = %+v, want one text part", parts[0])
		}
	})

	t.Run("structured parts are cloned", func(t *testing.T) {
		t.Parallel()

		message := LLMMessage{
			Role: LLMMessageRoleUser,
			Parts: []LLMMessagePart{
				{
					Type: LLMMessagePartTypeImage,
					Image: &LLMInputImage{
						MIMEType: "image/png",
						Data:     []byte{1, 2, 3},
					},
				},
			},
		}
		parts := message.ContentParts()
		parts[0].Image.Data[0] = 9
		if message.Parts[0].Image.Data[0] != 1 {
			t.Fatalf("original image data mutated: %v", message.Parts[0].Image.Data)
		}
	})
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

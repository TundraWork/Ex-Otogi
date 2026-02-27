package gemini

import (
	"context"
	"errors"
	"io"
	"iter"
	"strings"
	"testing"

	"ex-otogi/pkg/otogi"

	"google.golang.org/genai"
)

func TestNewGeminiProviderConfigValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		cfg              ProviderConfig
		wantErrSubstring string
	}{
		{
			name: "valid config",
			cfg: ProviderConfig{
				APIKey:           "gm-test",
				BaseURL:          "https://generativelanguage.googleapis.com/",
				APIVersion:       "v1beta",
				ThinkingLevel:    thinkingLevelMedium,
				ResponseMIMEType: responseMIMEJSON,
			},
		},
		{
			name: "missing api key",
			cfg: ProviderConfig{
				APIKey: "   ",
			},
			wantErrSubstring: "missing api_key",
		},
		{
			name: "invalid base url",
			cfg: ProviderConfig{
				APIKey:  "gm-test",
				BaseURL: "not a url",
			},
			wantErrSubstring: "parse base_url",
		},
		{
			name: "invalid api version",
			cfg: ProviderConfig{
				APIKey:     "gm-test",
				APIVersion: "v1 beta",
			},
			wantErrSubstring: "invalid api_version",
		},
		{
			name: "negative thinking budget",
			cfg: ProviderConfig{
				APIKey:         "gm-test",
				ThinkingBudget: ptrInt(-1),
			},
			wantErrSubstring: "thinking_budget",
		},
		{
			name: "invalid thinking level",
			cfg: ProviderConfig{
				APIKey:        "gm-test",
				ThinkingLevel: "minimal",
			},
			wantErrSubstring: "thinking_level",
		},
		{
			name: "invalid response mime type",
			cfg: ProviderConfig{
				APIKey:           "gm-test",
				ResponseMIMEType: "application/xml",
			},
			wantErrSubstring: "response_mime_type",
		},
		{
			name: "conflicting thinking options",
			cfg: ProviderConfig{
				APIKey:         "gm-test",
				ThinkingBudget: ptrInt(64),
				ThinkingLevel:  thinkingLevelMedium,
			},
			wantErrSubstring: "mutually exclusive",
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			provider, err := New(testCase.cfg)
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
				t.Fatalf("New failed: %v", err)
			}
			if provider == nil {
				t.Fatal("expected provider instance")
			}
		})
	}
}

func TestGeminiProviderGenerateStreamValidation(t *testing.T) {
	t.Parallel()

	provider := &Provider{
		models: &modelsClientStub{
			stream: emptySeq(),
		},
	}

	_, err := provider.GenerateStream(context.Background(), otogi.LLMGenerateRequest{
		Model: "gemini-2.5-flash",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "validate request") {
		t.Fatalf("error = %v, want validate request error", err)
	}
}

func TestGeminiProviderGenerateStreamMapsRequest(t *testing.T) {
	t.Parallel()

	client := &modelsClientStub{
		stream: seqFromSteps([]streamStep{
			{
				response: textResponse([]*genai.Part{
					{Text: "thought", Thought: true},
					{Text: "answer"},
				}),
			},
		}),
	}
	provider := &Provider{
		models: client,
		defaults: requestOptions{
			googleSearch:    ptrBool(false),
			urlContext:      ptrBool(false),
			includeThoughts: ptrBool(false),
		},
	}

	req := otogi.LLMGenerateRequest{
		Model: "gemini-2.5-flash",
		Messages: []otogi.LLMMessage{
			{Role: otogi.LLMMessageRoleSystem, Content: "sys-1"},
			{Role: otogi.LLMMessageRoleSystem, Content: "sys-2"},
			{Role: otogi.LLMMessageRoleUser, Content: "hello"},
			{Role: otogi.LLMMessageRoleAssistant, Content: "hi"},
		},
		MaxOutputTokens: 256,
		Temperature:     0.2,
		Metadata: map[string]string{
			metadataGoogleSearch:    "true",
			metadataURLContext:      "true",
			metadataThinkingBudget:  "128",
			metadataIncludeThoughts: "true",
			metadataResponseMIME:    responseMIMEJSON,
		},
	}

	stream, err := provider.GenerateStream(context.Background(), req)
	if err != nil {
		t.Fatalf("GenerateStream failed: %v", err)
	}
	if stream == nil {
		t.Fatal("expected stream")
	}

	if len(client.calls) != 1 {
		t.Fatalf("request count = %d, want 1", len(client.calls))
	}
	call := client.calls[0]
	if call.model != req.Model {
		t.Fatalf("model = %q, want %q", call.model, req.Model)
	}
	if len(call.contents) != 2 {
		t.Fatalf("contents len = %d, want 2", len(call.contents))
	}
	if call.contents[0].Role != string(genai.RoleUser) {
		t.Fatalf("contents[0] role = %q, want user", call.contents[0].Role)
	}
	if call.contents[1].Role != string(genai.RoleModel) {
		t.Fatalf("contents[1] role = %q, want model", call.contents[1].Role)
	}
	if call.config == nil {
		t.Fatal("expected generate config")
	}
	if call.config.SystemInstruction == nil || len(call.config.SystemInstruction.Parts) != 1 {
		t.Fatal("expected system instruction")
	}
	if call.config.SystemInstruction.Parts[0].Text != "sys-1\n\nsys-2" {
		t.Fatalf("system instruction = %q, want joined system prompts", call.config.SystemInstruction.Parts[0].Text)
	}
	if call.config.Temperature == nil || *call.config.Temperature != float32(req.Temperature) {
		t.Fatalf("temperature = %v, want %f", call.config.Temperature, req.Temperature)
	}
	if int(call.config.MaxOutputTokens) != req.MaxOutputTokens {
		t.Fatalf("max output tokens = %d, want %d", call.config.MaxOutputTokens, req.MaxOutputTokens)
	}
	if len(call.config.Tools) != 2 {
		t.Fatalf("tools len = %d, want 2", len(call.config.Tools))
	}
	if call.config.Tools[0].GoogleSearch == nil || call.config.Tools[1].URLContext == nil {
		t.Fatalf("tools = %+v, expected google search and url context", call.config.Tools)
	}
	if call.config.ThinkingConfig == nil {
		t.Fatal("expected thinking config")
	}
	if !call.config.ThinkingConfig.IncludeThoughts {
		t.Fatal("expected include thoughts true")
	}
	if call.config.ThinkingConfig.ThinkingBudget == nil || *call.config.ThinkingConfig.ThinkingBudget != 128 {
		t.Fatalf("thinking budget = %v, want 128", call.config.ThinkingConfig.ThinkingBudget)
	}
	if call.config.ThinkingConfig.ThinkingLevel != "" {
		t.Fatalf("thinking level = %q, want empty", call.config.ThinkingConfig.ThinkingLevel)
	}
	if call.config.ResponseMIMEType != responseMIMEJSON {
		t.Fatalf("response mime = %q, want %q", call.config.ResponseMIMEType, responseMIMEJSON)
	}
	if call.config.HTTPOptions == nil {
		t.Fatal("expected request http options")
	}
	if call.config.HTTPOptions.Timeout == nil {
		t.Fatal("expected request timeout override")
	}
	if *call.config.HTTPOptions.Timeout != 0 {
		t.Fatalf("request timeout = %s, want 0", *call.config.HTTPOptions.Timeout)
	}

	chunk, recvErr := stream.Recv(context.Background())
	if recvErr != nil {
		t.Fatalf("stream recv failed: %v", recvErr)
	}
	if chunk.Delta != "thought" {
		t.Fatalf("chunk delta = %q, want thought", chunk.Delta)
	}
	if chunk.Kind != otogi.LLMGenerateChunkKindThinkingSummary {
		t.Fatalf("chunk kind = %q, want %q", chunk.Kind, otogi.LLMGenerateChunkKindThinkingSummary)
	}

	chunk, recvErr = stream.Recv(context.Background())
	if recvErr != nil {
		t.Fatalf("stream recv failed: %v", recvErr)
	}
	if chunk.Delta != "answer" {
		t.Fatalf("chunk delta = %q, want answer", chunk.Delta)
	}
	if chunk.Kind != otogi.LLMGenerateChunkKindOutputText {
		t.Fatalf("chunk kind = %q, want %q", chunk.Kind, otogi.LLMGenerateChunkKindOutputText)
	}

	_, recvErr = stream.Recv(context.Background())
	if !errors.Is(recvErr, io.EOF) {
		t.Fatalf("stream recv error = %v, want io.EOF", recvErr)
	}
}

func TestGeminiProviderGenerateStreamInvalidMetadata(t *testing.T) {
	t.Parallel()

	provider := &Provider{
		models: &modelsClientStub{
			stream: emptySeq(),
		},
	}

	_, err := provider.GenerateStream(context.Background(), otogi.LLMGenerateRequest{
		Model: "gemini-2.5-flash",
		Messages: []otogi.LLMMessage{
			{Role: otogi.LLMMessageRoleUser, Content: "hello"},
		},
		Metadata: map[string]string{
			metadataThinkingBudget: "-1",
		},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), metadataThinkingBudget) {
		t.Fatalf("error = %v, want metadata key in error", err)
	}
}

func TestGeminiProviderGenerateStreamConflictingThinkingOptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		defaults         requestOptions
		metadata         map[string]string
		wantErrSubstring string
	}{
		{
			name: "metadata sets both",
			metadata: map[string]string{
				metadataThinkingBudget: "64",
				metadataThinkingLevel:  "high",
			},
			wantErrSubstring: "mutually exclusive",
		},
		{
			name: "defaults budget with metadata level",
			defaults: requestOptions{
				thinkingBudget: ptrInt32(32),
			},
			metadata: map[string]string{
				metadataThinkingLevel: "high",
			},
			wantErrSubstring: "mutually exclusive",
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			provider := &Provider{
				models:   &modelsClientStub{stream: emptySeq()},
				defaults: testCase.defaults,
			}
			_, err := provider.GenerateStream(context.Background(), otogi.LLMGenerateRequest{
				Model: "gemini-2.5-flash",
				Messages: []otogi.LLMMessage{
					{Role: otogi.LLMMessageRoleUser, Content: "hello"},
				},
				Metadata: testCase.metadata,
			})
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), testCase.wantErrSubstring) {
				t.Fatalf("error = %v, want substring %q", err, testCase.wantErrSubstring)
			}
		})
	}
}

func TestGeminiStreamEventsAndLifecycle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		steps            []streamStep
		includeThoughts  bool
		preCancelContext bool
		wantDelta        string
		wantKind         otogi.LLMGenerateChunkKind
		wantErrCheck     func(error) bool
	}{
		{
			name: "delta then completion",
			steps: []streamStep{
				{response: textResponse([]*genai.Part{{Text: "hello"}})},
			},
			wantDelta: "hello",
			wantKind:  otogi.LLMGenerateChunkKindOutputText,
			wantErrCheck: func(err error) bool {
				return err == nil
			},
		},
		{
			name: "skip empty then delta",
			steps: []streamStep{
				{response: &genai.GenerateContentResponse{}},
				{response: textResponse([]*genai.Part{{Text: "ok"}})},
			},
			wantDelta: "ok",
			wantKind:  otogi.LLMGenerateChunkKindOutputText,
			wantErrCheck: func(err error) bool {
				return err == nil
			},
		},
		{
			name: "thought filtered by default",
			steps: []streamStep{
				{response: textResponse([]*genai.Part{{Text: "hidden", Thought: true}, {Text: "shown"}})},
			},
			wantDelta: "shown",
			wantKind:  otogi.LLMGenerateChunkKindOutputText,
			wantErrCheck: func(err error) bool {
				return err == nil
			},
		},
		{
			name: "thought included when enabled",
			steps: []streamStep{
				{response: textResponse([]*genai.Part{{Text: "hidden", Thought: true}, {Text: "shown"}})},
			},
			includeThoughts: true,
			wantDelta:       "hidden",
			wantKind:        otogi.LLMGenerateChunkKindThinkingSummary,
			wantErrCheck: func(err error) bool {
				return err == nil
			},
		},
		{
			name: "stream error",
			steps: []streamStep{
				{err: errors.New("bad stream")},
			},
			wantErrCheck: func(err error) bool {
				return err != nil && strings.Contains(err.Error(), "bad stream")
			},
		},
		{
			name: "stream cancellation error",
			steps: []streamStep{
				{err: context.Canceled},
			},
			wantErrCheck: func(err error) bool {
				return errors.Is(err, context.Canceled)
			},
		},
		{
			name: "nil response",
			steps: []streamStep{
				{response: nil},
			},
			wantErrCheck: func(err error) bool {
				return err != nil && strings.Contains(err.Error(), "nil response")
			},
		},
		{
			name: "context canceled before recv",
			steps: []streamStep{
				{response: textResponse([]*genai.Part{{Text: "ignored"}})},
			},
			preCancelContext: true,
			wantErrCheck: func(err error) bool {
				return errors.Is(err, context.Canceled)
			},
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			stream := newGeminiStream(seqFromSteps(testCase.steps), testCase.includeThoughts)

			ctx := context.Background()
			if testCase.preCancelContext {
				canceledCtx, cancel := context.WithCancel(context.Background())
				cancel()
				ctx = canceledCtx
			}

			chunk, err := stream.Recv(ctx)
			if testCase.wantErrCheck != nil && !testCase.wantErrCheck(err) {
				t.Fatalf("Recv error = %v, unexpected", err)
			}
			if err == nil && chunk.Delta != testCase.wantDelta {
				t.Fatalf("chunk delta = %q, want %q", chunk.Delta, testCase.wantDelta)
			}
			if err == nil && testCase.wantKind != "" && chunk.Kind.Normalize() != testCase.wantKind {
				t.Fatalf("chunk kind = %q, want %q", chunk.Kind.Normalize(), testCase.wantKind)
			}
		})
	}
}

func TestGeminiStreamMixedPartOrdering(t *testing.T) {
	t.Parallel()

	stream := newGeminiStream(seqFromSteps([]streamStep{
		{
			response: textResponse([]*genai.Part{
				{Text: "thought-1", Thought: true},
				{Text: "answer-1"},
				{Text: "thought-2", Thought: true},
				{Text: "answer-2"},
			}),
		},
	}), true)

	want := []otogi.LLMGenerateChunk{
		{Kind: otogi.LLMGenerateChunkKindThinkingSummary, Delta: "thought-1"},
		{Kind: otogi.LLMGenerateChunkKindOutputText, Delta: "answer-1"},
		{Kind: otogi.LLMGenerateChunkKindThinkingSummary, Delta: "thought-2"},
		{Kind: otogi.LLMGenerateChunkKindOutputText, Delta: "answer-2"},
	}
	for index, expected := range want {
		chunk, err := stream.Recv(context.Background())
		if err != nil {
			t.Fatalf("Recv[%d] failed: %v", index, err)
		}
		if chunk.Kind != expected.Kind {
			t.Fatalf("Recv[%d] kind = %q, want %q", index, chunk.Kind, expected.Kind)
		}
		if chunk.Delta != expected.Delta {
			t.Fatalf("Recv[%d] delta = %q, want %q", index, chunk.Delta, expected.Delta)
		}
	}

	_, err := stream.Recv(context.Background())
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Recv after chunks error = %v, want io.EOF", err)
	}
}

func TestGeminiStreamCloseIdempotentAndPostCloseEOF(t *testing.T) {
	t.Parallel()

	stream := newGeminiStream(emptySeq(), false)

	if err := stream.Close(); err != nil {
		t.Fatalf("first close failed: %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("second close failed: %v", err)
	}

	_, err := stream.Recv(context.Background())
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Recv after close error = %v, want io.EOF", err)
	}
}

type modelsClientStub struct {
	calls  []generateCall
	stream iter.Seq2[*genai.GenerateContentResponse, error]
}

type generateCall struct {
	model    string
	contents []*genai.Content
	config   *genai.GenerateContentConfig
}

func (s *modelsClientStub) GenerateContentStream(
	_ context.Context,
	model string,
	contents []*genai.Content,
	config *genai.GenerateContentConfig,
) iter.Seq2[*genai.GenerateContentResponse, error] {
	s.calls = append(s.calls, generateCall{
		model:    model,
		contents: contents,
		config:   config,
	})
	if s.stream == nil {
		return emptySeq()
	}
	return s.stream
}

type streamStep struct {
	response *genai.GenerateContentResponse
	err      error
}

func seqFromSteps(steps []streamStep) iter.Seq2[*genai.GenerateContentResponse, error] {
	return func(yield func(*genai.GenerateContentResponse, error) bool) {
		for _, step := range steps {
			if !yield(step.response, step.err) {
				return
			}
		}
	}
}

func emptySeq() iter.Seq2[*genai.GenerateContentResponse, error] {
	return func(func(*genai.GenerateContentResponse, error) bool) {}
}

func textResponse(parts []*genai.Part) *genai.GenerateContentResponse {
	return &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{
			{
				Content: &genai.Content{
					Parts: parts,
				},
			},
		},
	}
}

func ptrBool(value bool) *bool {
	return &value
}

func ptrInt(value int) *int {
	return &value
}

func ptrInt32(value int32) *int32 {
	return &value
}

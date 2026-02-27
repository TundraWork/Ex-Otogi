package openai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"ex-otogi/pkg/otogi"

	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
)

func TestNewOpenAIProviderConfigValidation(t *testing.T) {
	t.Parallel()

	retries := 1
	tests := []struct {
		name             string
		cfg              ProviderConfig
		wantErrSubstring string
	}{
		{
			name: "valid config",
			cfg: ProviderConfig{
				APIKey:     "sk-test",
				BaseURL:    "https://api.openai.com/v1",
				MaxRetries: &retries,
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
				APIKey:  "sk-test",
				BaseURL: "not a url",
			},
			wantErrSubstring: "parse base_url",
		},
		{
			name: "negative retries",
			cfg: ProviderConfig{
				APIKey:     "sk-test",
				MaxRetries: ptrInt(-1),
			},
			wantErrSubstring: "max_retries must be >= 0",
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

func TestOpenAIProviderGenerateStreamValidation(t *testing.T) {
	t.Parallel()

	provider := &Provider{
		responses: &openAIResponsesClientStub{
			stream: &openAIResponseStreamStub{},
		},
	}

	_, err := provider.GenerateStream(context.Background(), otogi.LLMGenerateRequest{
		Model: "gpt-5-mini",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "validate request") {
		t.Fatalf("error = %v, want validate request error", err)
	}
}

func TestOpenAIProviderGenerateStreamMapsRequest(t *testing.T) {
	t.Parallel()

	client := &openAIResponsesClientStub{
		stream: &openAIResponseStreamStub{
			events: []responses.ResponseStreamEventUnion{
				mustUnmarshalEvent(t, `{
					"type":"response.completed",
					"sequence_number":1,
					"response":{}
				}`),
			},
		},
	}
	provider := &Provider{responses: client}

	req := otogi.LLMGenerateRequest{
		Model: "gpt-5-mini",
		Messages: []otogi.LLMMessage{
			{Role: otogi.LLMMessageRoleSystem, Content: "sys"},
			{Role: otogi.LLMMessageRoleUser, Content: "hello"},
			{Role: otogi.LLMMessageRoleAssistant, Content: "hi"},
		},
		MaxOutputTokens: 512,
		Temperature:     0.35,
		Metadata: map[string]string{
			"agent":                        "Otogi",
			metadataOpenAIReasoningSummary: "concise",
			metadataOpenAIReasoningEffort:  "low",
		},
	}
	stream, err := provider.GenerateStream(context.Background(), req)
	if err != nil {
		t.Fatalf("GenerateStream failed: %v", err)
	}
	if stream == nil {
		t.Fatal("expected stream")
	}

	if len(client.params) != 1 {
		t.Fatalf("request count = %d, want 1", len(client.params))
	}
	got := client.params[0]
	if got.Model != req.Model {
		t.Fatalf("model = %q, want %q", got.Model, req.Model)
	}
	if !got.Temperature.Valid() || got.Temperature.Value != req.Temperature {
		t.Fatalf("temperature = %+v, want %f", got.Temperature, req.Temperature)
	}
	if !got.MaxOutputTokens.Valid() || got.MaxOutputTokens.Value != int64(req.MaxOutputTokens) {
		t.Fatalf("max_output_tokens = %+v, want %d", got.MaxOutputTokens, req.MaxOutputTokens)
	}
	if got.Metadata["agent"] != "Otogi" {
		t.Fatalf("metadata agent = %q, want Otogi", got.Metadata["agent"])
	}
	if _, exists := got.Metadata[metadataOpenAIReasoningSummary]; exists {
		t.Fatalf("metadata contains %q control key", metadataOpenAIReasoningSummary)
	}
	if _, exists := got.Metadata[metadataOpenAIReasoningEffort]; exists {
		t.Fatalf("metadata contains %q control key", metadataOpenAIReasoningEffort)
	}
	if got.Reasoning.Summary != "concise" {
		t.Fatalf("reasoning summary = %q, want concise", got.Reasoning.Summary)
	}
	if got.Reasoning.Effort != "low" {
		t.Fatalf("reasoning effort = %q, want low", got.Reasoning.Effort)
	}

	if len(got.Input.OfInputItemList) != 3 {
		t.Fatalf("input messages len = %d, want 3", len(got.Input.OfInputItemList))
	}
	wantRoles := []string{"system", "user", "assistant"}
	for index, item := range got.Input.OfInputItemList {
		role := item.GetRole()
		if role == nil {
			t.Fatalf("input[%d] role is nil", index)
		}
		if *role != wantRoles[index] {
			t.Fatalf("input[%d] role = %q, want %q", index, *role, wantRoles[index])
		}
	}

	_, recvErr := stream.Recv(context.Background())
	if !errors.Is(recvErr, io.EOF) {
		t.Fatalf("stream recv error = %v, want io.EOF", recvErr)
	}
}

func TestOpenAIProviderGenerateStreamInvalidReasoningMetadata(t *testing.T) {
	t.Parallel()

	provider := &Provider{
		responses: &openAIResponsesClientStub{
			stream: &openAIResponseStreamStub{},
		},
	}

	tests := []struct {
		name             string
		metadata         map[string]string
		wantErrSubstring string
	}{
		{
			name: "invalid reasoning summary",
			metadata: map[string]string{
				metadataOpenAIReasoningSummary: "verbose",
			},
			wantErrSubstring: metadataOpenAIReasoningSummary,
		},
		{
			name: "invalid reasoning effort",
			metadata: map[string]string{
				metadataOpenAIReasoningEffort: "fast",
			},
			wantErrSubstring: metadataOpenAIReasoningEffort,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			_, err := provider.GenerateStream(context.Background(), otogi.LLMGenerateRequest{
				Model: "gpt-5-mini",
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

func TestOpenAIStreamEventsAndLifecycle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		events           []responses.ResponseStreamEventUnion
		streamErr        error
		preCancelContext bool
		wantDelta        string
		wantKind         otogi.LLMGenerateChunkKind
		wantErrCheck     func(error) bool
	}{
		{
			name: "delta then completion",
			events: []responses.ResponseStreamEventUnion{
				mustUnmarshalEvent(t, `{
					"type":"response.output_text.delta",
					"sequence_number":1,
					"item_id":"item-1",
					"output_index":0,
					"content_index":0,
					"delta":"hello",
					"logprobs":[]
				}`),
			},
			wantDelta: "hello",
			wantKind:  otogi.LLMGenerateChunkKindOutputText,
			wantErrCheck: func(err error) bool {
				return err == nil
			},
		},
		{
			name: "reasoning summary delta",
			events: []responses.ResponseStreamEventUnion{
				mustUnmarshalEvent(t, `{
					"type":"response.reasoning_summary_text.delta",
					"sequence_number":1,
					"item_id":"item-1",
					"output_index":0,
					"summary_index":0,
					"delta":"planning"
				}`),
			},
			wantDelta: "planning",
			wantKind:  otogi.LLMGenerateChunkKindThinkingSummary,
			wantErrCheck: func(err error) bool {
				return err == nil
			},
		},
		{
			name: "reasoning text hidden then output delta",
			events: []responses.ResponseStreamEventUnion{
				mustUnmarshalEvent(t, `{
					"type":"response.reasoning_text.delta",
					"sequence_number":1,
					"item_id":"item-1",
					"output_index":0,
					"content_index":0,
					"delta":"hidden"
				}`),
				mustUnmarshalEvent(t, `{
					"type":"response.output_text.delta",
					"sequence_number":2,
					"item_id":"item-1",
					"output_index":0,
					"content_index":1,
					"delta":"answer",
					"logprobs":[]
				}`),
			},
			wantDelta: "answer",
			wantKind:  otogi.LLMGenerateChunkKindOutputText,
			wantErrCheck: func(err error) bool {
				return err == nil
			},
		},
		{
			name: "skip non-text then delta",
			events: []responses.ResponseStreamEventUnion{
				mustUnmarshalEvent(t, `{"type":"response.in_progress","sequence_number":1,"response":{}}`),
				mustUnmarshalEvent(t, `{
					"type":"response.output_text.delta",
					"sequence_number":2,
					"item_id":"item-1",
					"output_index":0,
					"content_index":0,
					"delta":"ok",
					"logprobs":[]
				}`),
			},
			wantDelta: "ok",
			wantKind:  otogi.LLMGenerateChunkKindOutputText,
			wantErrCheck: func(err error) bool {
				return err == nil
			},
		},
		{
			name: "completion without deltas",
			events: []responses.ResponseStreamEventUnion{
				mustUnmarshalEvent(t, `{"type":"response.completed","sequence_number":1,"response":{}}`),
			},
			wantErrCheck: func(err error) bool {
				return errors.Is(err, io.EOF)
			},
		},
		{
			name: "error event",
			events: []responses.ResponseStreamEventUnion{
				mustUnmarshalEvent(t, `{
					"type":"error",
					"sequence_number":1,
					"code":"invalid_request_error",
					"message":"bad request",
					"param":""
				}`),
			},
			wantErrCheck: func(err error) bool {
				return err != nil && strings.Contains(err.Error(), "bad request")
			},
		},
		{
			name: "failed event",
			events: []responses.ResponseStreamEventUnion{
				mustUnmarshalEvent(t, `{
					"type":"response.failed",
					"sequence_number":1,
					"response":{"status":"failed"}
				}`),
			},
			wantErrCheck: func(err error) bool {
				return err != nil && strings.Contains(err.Error(), "response failed")
			},
		},
		{
			name: "malformed delta event",
			events: []responses.ResponseStreamEventUnion{
				mustUnmarshalEvent(t, `{
					"type":"response.output_text.delta",
					"sequence_number":1,
					"item_id":"item-1",
					"output_index":0,
					"content_index":0
				}`),
			},
			wantErrCheck: func(err error) bool {
				return err != nil && strings.Contains(err.Error(), "parse event")
			},
		},
		{
			name: "context canceled before recv",
			events: []responses.ResponseStreamEventUnion{
				mustUnmarshalEvent(t, `{
					"type":"response.output_text.delta",
					"sequence_number":1,
					"item_id":"item-1",
					"output_index":0,
					"content_index":0,
					"delta":"ignored",
					"logprobs":[]
				}`),
			},
			preCancelContext: true,
			wantErrCheck: func(err error) bool {
				return errors.Is(err, context.Canceled)
			},
		},
		{
			name:      "stream cancellation error",
			streamErr: context.Canceled,
			wantErrCheck: func(err error) bool {
				return errors.Is(err, context.Canceled)
			},
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			stub := &openAIResponseStreamStub{
				events: testCase.events,
				err:    testCase.streamErr,
			}
			stream := newOpenAIStream(stub)

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

func TestOpenAIStreamCloseIdempotentAndPostCloseEOF(t *testing.T) {
	t.Parallel()

	stub := &openAIResponseStreamStub{}
	stream := newOpenAIStream(stub)

	if err := stream.Close(); err != nil {
		t.Fatalf("first close failed: %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("second close failed: %v", err)
	}
	if stub.closeCount != 1 {
		t.Fatalf("close count = %d, want 1", stub.closeCount)
	}

	_, err := stream.Recv(context.Background())
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Recv after close error = %v, want io.EOF", err)
	}
}

func mustUnmarshalEvent(t *testing.T, raw string) responses.ResponseStreamEventUnion {
	t.Helper()

	var event responses.ResponseStreamEventUnion
	if err := json.Unmarshal([]byte(raw), &event); err != nil {
		t.Fatalf("unmarshal event failed: %v", err)
	}

	return event
}

func ptrInt(value int) *int {
	return &value
}

type openAIResponsesClientStub struct {
	params []responses.ResponseNewParams
	stream openAIResponseStream
}

func (s *openAIResponsesClientStub) NewStreaming(
	_ context.Context,
	body responses.ResponseNewParams,
	_ ...option.RequestOption,
) openAIResponseStream {
	s.params = append(s.params, body)
	if s.stream == nil {
		return &openAIResponseStreamStub{}
	}

	return s.stream
}

type openAIResponseStreamStub struct {
	events []responses.ResponseStreamEventUnion
	err    error

	current    responses.ResponseStreamEventUnion
	index      int
	closeCount int
}

func (s *openAIResponseStreamStub) Next() bool {
	if s.index >= len(s.events) {
		return false
	}

	s.current = s.events[s.index]
	s.index++
	return true
}

func (s *openAIResponseStreamStub) Current() responses.ResponseStreamEventUnion {
	return s.current
}

func (s *openAIResponseStreamStub) Err() error {
	return s.err
}

func (s *openAIResponseStreamStub) Close() error {
	s.closeCount++
	return nil
}

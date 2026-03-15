package openai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"ex-otogi/pkg/otogi/ai"

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

	_, err := provider.GenerateStream(context.Background(), ai.LLMGenerateRequest{
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

	req := ai.LLMGenerateRequest{
		Model: "gpt-5-mini",
		Messages: []ai.LLMMessage{
			{Role: ai.LLMMessageRoleSystem, Content: "sys"},
			{
				Role: ai.LLMMessageRoleUser,
				Parts: []ai.LLMMessagePart{
					{Type: ai.LLMMessagePartTypeText, Text: "hello"},
					{
						Type: ai.LLMMessagePartTypeImage,
						Image: &ai.LLMInputImage{
							MIMEType: "image/png",
							Data:     []byte{1, 2, 3, 4},
							Detail:   ai.LLMInputImageDetailHigh,
						},
					},
				},
			},
			{Role: ai.LLMMessageRoleAssistant, Content: "hi"},
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
	contentAny := got.Input.OfInputItemList[1].GetContent().AsAny()
	content, ok := contentAny.(*responses.ResponseInputMessageContentListParam)
	if !ok {
		t.Fatalf("user content type = %T, want *responses.ResponseInputMessageContentListParam", contentAny)
	}
	if len(*content) != 2 {
		t.Fatalf("user content len = %d, want 2", len(*content))
	}
	if gotText := (*content)[0].GetText(); gotText == nil || *gotText != "hello" {
		t.Fatalf("user content[0] text = %v, want hello", gotText)
	}
	if gotDetail := (*content)[1].GetDetail(); gotDetail == nil || *gotDetail != "high" {
		t.Fatalf("user content[1] detail = %v, want high", gotDetail)
	}
	if gotURL := (*content)[1].GetImageURL(); gotURL == nil || !strings.HasPrefix(*gotURL, "data:image/png;base64,") {
		t.Fatalf("user content[1] image_url = %v, want data URL", gotURL)
	}

	_, recvErr := stream.Recv(context.Background())
	if !errors.Is(recvErr, io.EOF) {
		t.Fatalf("stream recv error = %v, want io.EOF", recvErr)
	}
}

func TestOpenAIProviderGenerateStreamMapsToolsAndToolMessages(t *testing.T) {
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

	req := ai.LLMGenerateRequest{
		Model: "gpt-5-mini",
		Messages: []ai.LLMMessage{
			{Role: ai.LLMMessageRoleUser, Content: "remember this"},
			{
				Role: ai.LLMMessageRoleAssistant,
				ToolCalls: []ai.LLMToolCall{
					{
						ID:        "call-1",
						Name:      "remember",
						Arguments: `{"content":"hello"}`,
					},
				},
			},
			{
				Role:       ai.LLMMessageRoleTool,
				ToolCallID: "call-1",
				Content:    `{"result":"stored"}`,
			},
		},
		Tools: []ai.LLMToolDefinition{
			{
				Name:        "remember",
				Description: "Store a fact.",
				Parameters:  []byte(`{"type":"object","properties":{"content":{"type":"string"}}}`),
			},
		},
	}

	stream, err := provider.GenerateStream(context.Background(), req)
	if err != nil {
		t.Fatalf("GenerateStream failed: %v", err)
	}
	if stream == nil {
		t.Fatal("expected stream")
	}

	got := client.params[0]
	if len(got.Tools) != 1 {
		t.Fatalf("tools len = %d, want 1", len(got.Tools))
	}
	if got.Tools[0].OfFunction == nil {
		t.Fatal("expected function tool definition")
	}
	if got.Tools[0].OfFunction.Name != "remember" {
		t.Fatalf("tool name = %q, want remember", got.Tools[0].OfFunction.Name)
	}
	if !got.Tools[0].OfFunction.Strict.Valid() || !got.Tools[0].OfFunction.Strict.Value {
		t.Fatalf("tool strict = %+v, want true", got.Tools[0].OfFunction.Strict)
	}
	if len(got.Input.OfInputItemList) != 3 {
		t.Fatalf("input items len = %d, want 3", len(got.Input.OfInputItemList))
	}
	if got.Input.OfInputItemList[1].OfFunctionCall == nil {
		t.Fatal("input[1] is not a function call item")
	}
	if callID := got.Input.OfInputItemList[1].GetCallID(); callID == nil || *callID != "call-1" {
		t.Fatalf("input[1] call_id = %v, want call-1", callID)
	}
	if arguments := got.Input.OfInputItemList[1].GetArguments(); arguments == nil || *arguments != `{"content":"hello"}` {
		t.Fatalf("input[1] arguments = %v, want JSON arguments", arguments)
	}
	if got.Input.OfInputItemList[2].OfFunctionCallOutput == nil {
		t.Fatal("input[2] is not a function call output item")
	}
	if callID := got.Input.OfInputItemList[2].GetCallID(); callID == nil || *callID != "call-1" {
		t.Fatalf("input[2] call_id = %v, want call-1", callID)
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

			_, err := provider.GenerateStream(context.Background(), ai.LLMGenerateRequest{
				Model: "gpt-5-mini",
				Messages: []ai.LLMMessage{
					{Role: ai.LLMMessageRoleUser, Content: "hello"},
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
		wantKind         ai.LLMGenerateChunkKind
		wantToolCallID   string
		wantToolName     string
		wantErrCheck     func(error) bool
	}{
		{
			name: "tool call added event",
			events: []responses.ResponseStreamEventUnion{
				mustUnmarshalEvent(t, `{
					"type":"response.output_item.added",
					"sequence_number":1,
					"output_index":0,
					"item":{
						"type":"function_call",
						"id":"item-1",
						"call_id":"call-1",
						"name":"remember",
						"arguments":"",
						"status":"in_progress"
					}
				}`),
			},
			wantKind:       ai.LLMGenerateChunkKindToolCall,
			wantToolCallID: "call-1",
			wantToolName:   "remember",
			wantErrCheck: func(err error) bool {
				return err == nil
			},
		},
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
			wantKind:  ai.LLMGenerateChunkKindOutputText,
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
			wantKind:  ai.LLMGenerateChunkKindThinkingSummary,
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
			wantKind:  ai.LLMGenerateChunkKindOutputText,
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
			wantKind:  ai.LLMGenerateChunkKindOutputText,
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
			if err == nil && testCase.wantToolCallID != "" && chunk.ToolCallID != testCase.wantToolCallID {
				t.Fatalf("chunk tool_call_id = %q, want %q", chunk.ToolCallID, testCase.wantToolCallID)
			}
			if err == nil && testCase.wantToolName != "" && chunk.ToolCallName != testCase.wantToolName {
				t.Fatalf("chunk tool_call_name = %q, want %q", chunk.ToolCallName, testCase.wantToolName)
			}
		})
	}
}

func TestOpenAIStreamToolCallArgumentsDeltaUsesCallID(t *testing.T) {
	t.Parallel()

	stream := newOpenAIStream(&openAIResponseStreamStub{
		events: []responses.ResponseStreamEventUnion{
			mustUnmarshalEvent(t, `{
				"type":"response.output_item.added",
				"sequence_number":1,
				"output_index":0,
				"item":{
					"type":"function_call",
					"id":"item-1",
					"call_id":"call-1",
					"name":"remember",
					"arguments":"",
					"status":"in_progress"
				}
			}`),
			mustUnmarshalEvent(t, `{
				"type":"response.function_call_arguments.delta",
				"sequence_number":2,
				"item_id":"item-1",
				"output_index":0,
				"delta":"{\"content\":\"hello\"}"
			}`),
		},
	})

	chunk, err := stream.Recv(context.Background())
	if err != nil {
		t.Fatalf("Recv failed: %v", err)
	}
	if chunk.Kind != ai.LLMGenerateChunkKindToolCall || chunk.ToolCallID != "call-1" || chunk.ToolCallName != "remember" {
		t.Fatalf("first chunk = %+v, want tool call metadata", chunk)
	}

	chunk, err = stream.Recv(context.Background())
	if err != nil {
		t.Fatalf("Recv failed: %v", err)
	}
	if chunk.Kind != ai.LLMGenerateChunkKindToolCall {
		t.Fatalf("second chunk kind = %q, want tool_call", chunk.Kind)
	}
	if chunk.ToolCallID != "call-1" {
		t.Fatalf("second chunk tool_call_id = %q, want call-1", chunk.ToolCallID)
	}
	if chunk.ToolCallName != "remember" {
		t.Fatalf("second chunk tool_call_name = %q, want remember", chunk.ToolCallName)
	}
	if chunk.ToolCallArguments != `{"content":"hello"}` {
		t.Fatalf("second chunk arguments = %q, want JSON delta", chunk.ToolCallArguments)
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

func TestOpenAIStreamCloseDoesNotBlockWhileRecvWaitsOnNext(t *testing.T) {
	t.Parallel()

	stub := newBlockingOpenAIResponseStreamStub()
	stream := newOpenAIStream(stub)
	recvDone := make(chan error, 1)

	go func() {
		_, err := stream.Recv(context.Background())
		recvDone <- err
	}()

	select {
	case <-stub.nextStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Recv to enter Next")
	}

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- stream.Close()
	}()

	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("close failed: %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("close blocked while Next was in progress")
	}

	select {
	case <-stub.closeCalled:
	case <-time.After(time.Second):
		t.Fatal("expected stream close to be forwarded")
	}

	close(stub.unblockNext)

	select {
	case err := <-recvDone:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("Recv after close = %v, want io.EOF", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Recv did not return after unblocking Next")
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

type blockingOpenAIResponseStreamStub struct {
	nextStarted chan struct{}
	unblockNext chan struct{}
	closeCalled chan struct{}
}

func newBlockingOpenAIResponseStreamStub() *blockingOpenAIResponseStreamStub {
	return &blockingOpenAIResponseStreamStub{
		nextStarted: make(chan struct{}),
		unblockNext: make(chan struct{}),
		closeCalled: make(chan struct{}),
	}
}

func (s *blockingOpenAIResponseStreamStub) Next() bool {
	select {
	case <-s.nextStarted:
	default:
		close(s.nextStarted)
	}

	<-s.unblockNext
	return false
}

func (s *blockingOpenAIResponseStreamStub) Current() responses.ResponseStreamEventUnion {
	return responses.ResponseStreamEventUnion{}
}

func (s *blockingOpenAIResponseStreamStub) Err() error {
	return nil
}

func (s *blockingOpenAIResponseStreamStub) Close() error {
	select {
	case <-s.closeCalled:
	default:
		close(s.closeCalled)
	}

	return nil
}

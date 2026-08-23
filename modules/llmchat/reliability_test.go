package llmchat

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"ex-otogi/pkg/otogi/ai"
	panel "ex-otogi/pkg/otogi/management"
	"ex-otogi/pkg/otogi/platform"
)

func TestChatReliabilityScenarios(t *testing.T) {
	t.Run("partial visible answer is not regenerated or overwritten", func(t *testing.T) {
		module := newTestModule(validModuleConfig())
		sink := &sinkDispatcherStub{}
		module.dispatcher = sink
		module.clock = func() time.Time { return time.Unix(0, 0).UTC() }
		retryErr := ai.NewLLMFailure(ai.LLMFailureProviderUnavailable, true, time.Millisecond, errors.New("connection lost"))
		provider := &providerStub{streams: []ai.LLMStream{
			&stepStream{steps: []streamStep{{chunk: ai.LLMGenerateChunk{Delta: "partial answer"}}, {err: retryErr}}},
			&streamStub{chunks: []ai.LLMGenerateChunk{{Delta: "replacement"}}},
		}}
		req := ai.LLMGenerateRequest{Model: "gpt-test", Messages: []ai.LLMMessage{{Role: ai.LLMMessageRoleUser, Content: "hello"}}}
		err := module.streamProviderReply(context.Background(), context.Background(), testTarget(), "placeholder-1", provider, req)
		if err == nil {
			t.Fatal("stream error = nil, want terminal partial-output failure")
		}
		if len(provider.requests) != 1 {
			t.Fatalf("provider requests = %d, want no regeneration", len(provider.requests))
		}
		if len(sink.editRequests) != 1 || sink.editRequests[0].Text != "partial answer" {
			t.Fatalf("edits = %#v, want preserved partial answer", sink.editRequests)
		}

		if finalizedErr := module.finalizePlaceholderFailure(context.Background(), testTarget(), "placeholder-1", err); !errors.Is(finalizedErr, err) {
			t.Fatalf("finalize error = %v, want stream error", finalizedErr)
		}
		if len(sink.editRequests) != 1 {
			t.Fatalf("edits after failure finalization = %d, want partial answer preserved", len(sink.editRequests))
		}
	})

	t.Run("executed tool is not replayed when follow-up generation retries", func(t *testing.T) {
		module := newTestModule(validModuleConfig())
		module.dispatcher = &sinkDispatcherStub{}
		module.clock = func() time.Time { return time.Unix(0, 0).UTC() }
		executions := 0
		registry := NewToolRegistry([]ToolHandler{&toolHandlerStub{
			name: "remember", definition: rememberToolDefinition(),
			execute: func(context.Context, json.RawMessage) (string, error) {
				executions++
				return "{\"status\":\"ok\"}", nil
			},
		}})
		retryErr := ai.NewLLMFailure(ai.LLMFailureProviderUnavailable, true, time.Millisecond, errors.New("provider unavailable"))
		provider := &providerStub{streams: []ai.LLMStream{
			&streamStub{chunks: []ai.LLMGenerateChunk{{
				Kind: ai.LLMGenerateChunkKindToolCall, ToolCallID: "call-1", ToolCallName: "remember",
				ToolCallArguments: "{\"content\":\"hello\",\"category\":\"knowledge\"}",
			}}},
			&streamStub{recvErr: retryErr},
			&streamStub{chunks: []ai.LLMGenerateChunk{{Delta: "final answer"}}},
		}}
		req := ai.LLMGenerateRequest{
			Model: "gpt-test", Messages: []ai.LLMMessage{{Role: ai.LLMMessageRoleUser, Content: "remember this"}},
			Tools: registry.Definitions(),
		}
		if err := module.streamProviderReplyWithTools(context.Background(), context.Background(), testTarget(), "placeholder-1", provider, req, registry); err != nil {
			t.Fatalf("streamProviderReplyWithTools failed: %v", err)
		}
		if executions != 1 {
			t.Fatalf("tool executions = %d, want exactly one", executions)
		}
		if len(provider.requests) != 3 {
			t.Fatalf("provider requests = %d, want tool response plus two follow-up attempts", len(provider.requests))
		}
	})

	t.Run("classified failures have stable non-generic outcomes", func(t *testing.T) {
		classes := []ai.LLMFailureClass{
			ai.LLMFailureInvalidRequest, ai.LLMFailureAuthentication, ai.LLMFailureRateLimited,
			ai.LLMFailureProviderUnavailable, ai.LLMFailureTimeout, ai.LLMFailureCanceled,
			ai.LLMFailureSafetyRefusal, ai.LLMFailureEmptyResponse, ai.LLMFailureTool,
		}
		ctx := panel.WithTrace(context.Background(), panel.TraceContext{TraceID: "trc_1234567890abcdef"})
		for _, class := range classes {
			message := chatFailureMessage(ctx, class)
			if message == placeholderFailureMessage {
				t.Fatalf("class %s used generic fallback", class)
			}
			if !strings.Contains(message, "Reference: 90abcdef") {
				t.Fatalf("class %s message = %q, want short correlation reference", class, message)
			}
		}
	})
}

type streamStep struct {
	chunk ai.LLMGenerateChunk
	err   error
}

type stepStream struct {
	steps []streamStep
	index int
}

func (s *stepStream) Recv(context.Context) (ai.LLMGenerateChunk, error) {
	if s.index >= len(s.steps) {
		return ai.LLMGenerateChunk{}, io.EOF
	}
	step := s.steps[s.index]
	s.index++
	return step.chunk, step.err
}

func (*stepStream) Close() error { return nil }

func testTarget() platform.OutboundTarget {
	return platform.OutboundTarget{Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup}}
}

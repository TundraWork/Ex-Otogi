package llmchat

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"ex-otogi/pkg/otogi/ai"
	"ex-otogi/pkg/otogi/core"
	"ex-otogi/pkg/otogi/platform"
)

func TestBuildGenerateRequestWithToolsIncludesDefinitions(t *testing.T) {
	module := newTestModule(Config{
		RequestTimeout: time.Second,
		Agents: []Agent{
			{
				Name:                 "Otogi",
				Description:          "assistant",
				Provider:             "openai",
				Model:                "gpt-test",
				SystemPromptTemplate: "You are {{.AgentName}}",
				RequestTimeout:       time.Second,
			},
		},
	})
	module.memory = &memoryStub{
		replyChain: []core.ReplyChainEntry{
			{
				Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup},
				Actor:        platform.Actor{ID: "user-1", DisplayName: "Alice"},
				Article:      platform.Article{ID: "m-1", Text: "Otogi remember this"},
				IsCurrent:    true,
			},
		},
	}
	module.clock = func() time.Time { return time.Unix(100, 0).UTC() }

	registry := NewToolRegistry([]ToolHandler{
		&toolHandlerStub{
			name:       "remember",
			definition: rememberToolDefinition(),
		},
	})

	req, err := module.buildGenerateRequestWithTools(
		context.Background(),
		testLLMChatEvent("Otogi remember this"),
		module.cfg.Agents[0],
		"remember this",
		registry,
	)
	if err != nil {
		t.Fatalf("buildGenerateRequestWithTools failed: %v", err)
	}

	if len(req.Tools) != 1 {
		t.Fatalf("tools len = %d, want 1", len(req.Tools))
	}
	if req.Tools[0].Name != "remember" {
		t.Fatalf("tool name = %q, want remember", req.Tools[0].Name)
	}
	if string(req.Tools[0].Parameters) != string(rememberToolDefinition().Parameters) {
		t.Fatalf("tool parameters = %s, want %s", req.Tools[0].Parameters, rememberToolDefinition().Parameters)
	}
}

func TestStreamProviderReplyWithToolsReinvokesProviderWithToolResults(t *testing.T) {
	module := newTestModule(validModuleConfig())
	sink := &sinkDispatcherStub{}
	module.dispatcher = sink
	module.clock = func() time.Time { return time.Unix(0, 0).UTC() }

	tool := &toolHandlerStub{
		name:       "remember",
		definition: rememberToolDefinition(),
		execute: func(_ context.Context, args json.RawMessage) (string, error) {
			if string(args) != `{"content":"hello","category":"knowledge"}` {
				t.Fatalf("tool args = %s, want semantic memory payload", args)
			}
			return `{"status":"ok"}`, nil
		},
	}
	registry := NewToolRegistry([]ToolHandler{tool})

	provider := &providerStub{
		streams: []ai.LLMStream{
			&streamStub{chunks: []ai.LLMGenerateChunk{
				{Kind: ai.LLMGenerateChunkKindToolCall, ToolCallID: "call-1", ToolCallName: "remember"},
				{Kind: ai.LLMGenerateChunkKindToolCall, ToolCallID: "call-1", ToolCallArguments: `{"content":"hello"`},
				{Kind: ai.LLMGenerateChunkKindToolCall, ToolCallID: "call-1", ToolCallArguments: `,"category":"knowledge"}`},
			}},
			&streamStub{chunks: []ai.LLMGenerateChunk{
				{Delta: "final answer"},
			}},
		},
	}
	req := ai.LLMGenerateRequest{
		Model: "gpt-test",
		Messages: []ai.LLMMessage{
			{Role: ai.LLMMessageRoleSystem, Content: "sys"},
			{Role: ai.LLMMessageRoleUser, Content: "u"},
		},
		Tools: registry.Definitions(),
	}

	if err := module.streamProviderReplyWithTools(
		context.Background(),
		context.Background(),
		platform.OutboundTarget{Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup}},
		"placeholder-1",
		provider,
		req,
		registry,
	); err != nil {
		t.Fatalf("streamProviderReplyWithTools failed: %v", err)
	}

	if len(provider.requests) != 2 {
		t.Fatalf("provider request count = %d, want 2", len(provider.requests))
	}
	if len(provider.requests[1].Messages) != 4 {
		t.Fatalf("follow-up message count = %d, want 4", len(provider.requests[1].Messages))
	}
	assistant := provider.requests[1].Messages[2]
	if assistant.Role != ai.LLMMessageRoleAssistant {
		t.Fatalf("assistant role = %q, want assistant", assistant.Role)
	}
	if len(assistant.ToolCalls) != 1 {
		t.Fatalf("assistant tool_calls len = %d, want 1", len(assistant.ToolCalls))
	}
	if assistant.ToolCalls[0].ID != "call-1" || assistant.ToolCalls[0].Name != "remember" {
		t.Fatalf("assistant tool call = %+v, want call-1 remember", assistant.ToolCalls[0])
	}
	if assistant.ToolCalls[0].Arguments != `{"content":"hello","category":"knowledge"}` {
		t.Fatalf("assistant tool arguments = %q, want merged json", assistant.ToolCalls[0].Arguments)
	}

	toolMessage := provider.requests[1].Messages[3]
	if toolMessage.Role != ai.LLMMessageRoleTool {
		t.Fatalf("tool message role = %q, want tool", toolMessage.Role)
	}
	if toolMessage.ToolCallID != "call-1" {
		t.Fatalf("tool message tool_call_id = %q, want call-1", toolMessage.ToolCallID)
	}
	if toolMessage.Content != `{"status":"ok"}` {
		t.Fatalf("tool message content = %q, want tool result", toolMessage.Content)
	}

	if len(sink.editRequests) != 2 {
		t.Fatalf("edit request count = %d, want 2", len(sink.editRequests))
	}
	if sink.editRequests[0].Text != toolExecutionPlaceholder {
		t.Fatalf("status edit text = %q, want %q", sink.editRequests[0].Text, toolExecutionPlaceholder)
	}
	if sink.editRequests[1].Text != "final answer" {
		t.Fatalf("final edit text = %q, want final answer", sink.editRequests[1].Text)
	}
}

func TestStreamProviderReplyWithToolsStopsAtMaxIterations(t *testing.T) {
	module := newTestModule(validModuleConfig())
	module.dispatcher = &sinkDispatcherStub{}
	module.clock = func() time.Time { return time.Unix(0, 0).UTC() }

	streams := make([]ai.LLMStream, 0, maxToolIterations)
	for index := 0; index < maxToolIterations; index++ {
		streams = append(streams, &streamStub{chunks: []ai.LLMGenerateChunk{
			{Kind: ai.LLMGenerateChunkKindToolCall, ToolCallID: "call-1", ToolCallName: "remember", ToolCallArguments: `{"content":"hello","category":"knowledge"}`},
		}})
	}

	registry := NewToolRegistry([]ToolHandler{
		&toolHandlerStub{
			name:       "remember",
			definition: rememberToolDefinition(),
			execute: func(context.Context, json.RawMessage) (string, error) {
				return `{"status":"ok"}`, nil
			},
		},
	})
	provider := &providerStub{streams: streams}

	err := module.streamProviderReplyWithTools(
		context.Background(),
		context.Background(),
		platform.OutboundTarget{Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup}},
		"placeholder-1",
		provider,
		ai.LLMGenerateRequest{
			Model: "gpt-test",
			Messages: []ai.LLMMessage{
				{Role: ai.LLMMessageRoleSystem, Content: "sys"},
				{Role: ai.LLMMessageRoleUser, Content: "u"},
			},
			Tools: registry.Definitions(),
		},
		registry,
	)
	if err == nil {
		t.Fatal("streamProviderReplyWithTools error = nil, want max tool iteration error")
	}
	if !strings.Contains(err.Error(), "exceeded max tool iterations") {
		t.Fatalf("error = %q, want max tool iterations", err)
	}
	if len(provider.requests) != maxToolIterations {
		t.Fatalf("provider request count = %d, want %d", len(provider.requests), maxToolIterations)
	}
}

func TestStreamProviderReplyWithToolsPreservesThoughtSignature(t *testing.T) {
	module := newTestModule(validModuleConfig())
	sink := &sinkDispatcherStub{}
	module.dispatcher = sink
	module.clock = func() time.Time { return time.Unix(0, 0).UTC() }

	tool := &toolHandlerStub{
		name:       "remember",
		definition: rememberToolDefinition(),
		execute: func(_ context.Context, _ json.RawMessage) (string, error) {
			return `{"status":"ok"}`, nil
		},
	}
	registry := NewToolRegistry([]ToolHandler{tool})

	signature := []byte("opaque-thought-sig-test")
	provider := &providerStub{
		streams: []ai.LLMStream{
			&streamStub{chunks: []ai.LLMGenerateChunk{
				{
					Kind:                     ai.LLMGenerateChunkKindToolCall,
					ToolCallID:               "call-1",
					ToolCallName:             "remember",
					ToolCallArguments:        `{"content":"hello","category":"knowledge"}`,
					ToolCallThoughtSignature: signature,
				},
			}},
			&streamStub{chunks: []ai.LLMGenerateChunk{
				{Delta: "final answer"},
			}},
		},
	}
	req := ai.LLMGenerateRequest{
		Model: "gpt-test",
		Messages: []ai.LLMMessage{
			{Role: ai.LLMMessageRoleSystem, Content: "sys"},
			{Role: ai.LLMMessageRoleUser, Content: "u"},
		},
		Tools: registry.Definitions(),
	}

	if err := module.streamProviderReplyWithTools(
		context.Background(),
		context.Background(),
		platform.OutboundTarget{Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup}},
		"placeholder-1",
		provider,
		req,
		registry,
	); err != nil {
		t.Fatalf("streamProviderReplyWithTools failed: %v", err)
	}

	if len(provider.requests) != 2 {
		t.Fatalf("provider request count = %d, want 2", len(provider.requests))
	}
	assistant := provider.requests[1].Messages[2]
	if len(assistant.ToolCalls) != 1 {
		t.Fatalf("assistant tool_calls len = %d, want 1", len(assistant.ToolCalls))
	}
	if len(assistant.ToolCalls[0].ThoughtSignature) == 0 {
		t.Fatal("expected thought signature on propagated tool call")
	}
	if string(assistant.ToolCalls[0].ThoughtSignature) != string(signature) {
		t.Fatalf("thought signature = %q, want %q", assistant.ToolCalls[0].ThoughtSignature, signature)
	}
}

func TestStreamProviderReplyWithToolsReturnsToolExecutionError(t *testing.T) {
	module := newTestModule(validModuleConfig())
	module.dispatcher = &sinkDispatcherStub{}
	module.clock = func() time.Time { return time.Unix(0, 0).UTC() }

	registry := NewToolRegistry([]ToolHandler{
		&toolHandlerStub{
			name:       "remember",
			definition: rememberToolDefinition(),
			execute: func(context.Context, json.RawMessage) (string, error) {
				return "", errors.New("boom")
			},
		},
	})
	provider := &providerStub{
		stream: &streamStub{chunks: []ai.LLMGenerateChunk{
			{Kind: ai.LLMGenerateChunkKindToolCall, ToolCallID: "call-1", ToolCallName: "remember", ToolCallArguments: `{"content":"hello","category":"knowledge"}`},
		}},
	}

	err := module.streamProviderReplyWithTools(
		context.Background(),
		context.Background(),
		platform.OutboundTarget{Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup}},
		"placeholder-1",
		provider,
		ai.LLMGenerateRequest{
			Model: "gpt-test",
			Messages: []ai.LLMMessage{
				{Role: ai.LLMMessageRoleSystem, Content: "sys"},
				{Role: ai.LLMMessageRoleUser, Content: "u"},
			},
			Tools: registry.Definitions(),
		},
		registry,
	)
	if err == nil {
		t.Fatal("streamProviderReplyWithTools error = nil, want tool execution error")
	}
	if !strings.Contains(err.Error(), "execute tools") {
		t.Fatalf("error = %q, want execute tools context", err)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("provider request count = %d, want 1", len(provider.requests))
	}
}

type toolHandlerStub struct {
	name       string
	definition ai.LLMToolDefinition
	execute    func(ctx context.Context, args json.RawMessage) (string, error)
}

func (s *toolHandlerStub) Name() string {
	if strings.TrimSpace(s.name) != "" {
		return s.name
	}
	return s.definition.Name
}

func (s *toolHandlerStub) Definition() ai.LLMToolDefinition {
	definition := s.definition
	if strings.TrimSpace(definition.Name) == "" {
		definition.Name = s.Name()
	}
	return definition
}

func (s *toolHandlerStub) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	if s.execute == nil {
		return `{"status":"ok"}`, nil
	}
	return s.execute(ctx, args)
}

func rememberToolDefinition() ai.LLMToolDefinition {
	return ai.LLMToolDefinition{
		Name:        "remember",
		Description: "Persist one fact for later retrieval.",
		Parameters: json.RawMessage(`{
			"type":"object",
			"properties":{
				"content":{"type":"string"},
				"category":{"type":"string"}
			},
			"required":["content","category"],
			"additionalProperties":false
		}`),
	}
}

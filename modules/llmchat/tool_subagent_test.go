package llmchat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"ex-otogi/pkg/otogi/ai"
	"ex-otogi/pkg/otogi/platform"
)

func TestSubAgentToolExecute(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		cfg              SubAgentConfig
		provider         ai.LLMProvider
		args             json.RawMessage
		wantResult       string
		wantErrSubstring string
		assertRequest    func(*testing.T, ai.LLMGenerateRequest)
	}{
		{
			name: "successful execution returns collected text",
			cfg:  validSubAgentConfig(),
			provider: &providerStub{
				stream: &streamStub{chunks: []ai.LLMGenerateChunk{
					{Kind: ai.LLMGenerateChunkKindOutputText, Delta: "search "},
					{Kind: ai.LLMGenerateChunkKindOutputText, Delta: "result"},
				}},
			},
			args:       json.RawMessage(`{"query":"golang concurrency"}`),
			wantResult: "search result",
		},
		{
			name: "thinking chunks are skipped",
			cfg:  validSubAgentConfig(),
			provider: &providerStub{
				stream: &streamStub{chunks: []ai.LLMGenerateChunk{
					{Kind: ai.LLMGenerateChunkKindThinkingSummary, Delta: "thinking about it"},
					{Kind: ai.LLMGenerateChunkKindOutputText, Delta: "answer"},
				}},
			},
			args:       json.RawMessage(`{"query":"test"}`),
			wantResult: "answer",
		},
		{
			name: "tool call chunks are skipped",
			cfg:  validSubAgentConfig(),
			provider: &providerStub{
				stream: &streamStub{chunks: []ai.LLMGenerateChunk{
					{Kind: ai.LLMGenerateChunkKindToolCall, ToolCallID: "c1", ToolCallName: "fn"},
					{Kind: ai.LLMGenerateChunkKindOutputText, Delta: "output"},
				}},
			},
			args:       json.RawMessage(`{"query":"test"}`),
			wantResult: "output",
		},
		{
			name: "request has nil tools",
			cfg:  validSubAgentConfig(),
			provider: &providerStub{
				stream: &streamStub{chunks: []ai.LLMGenerateChunk{
					{Kind: ai.LLMGenerateChunkKindOutputText, Delta: "ok"},
				}},
			},
			args: json.RawMessage(`{"query":"test"}`),
			assertRequest: func(t *testing.T, req ai.LLMGenerateRequest) {
				t.Helper()
				if req.Tools != nil {
					t.Fatalf("Tools = %v, want nil", req.Tools)
				}
			},
			wantResult: "ok",
		},
		{
			name: "request metadata is passed through",
			cfg: func() SubAgentConfig {
				cfg := validSubAgentConfig()
				cfg.RequestMetadata = map[string]string{"gemini.google_search": "true"}
				return cfg
			}(),
			provider: &providerStub{
				stream: &streamStub{chunks: []ai.LLMGenerateChunk{
					{Kind: ai.LLMGenerateChunkKindOutputText, Delta: "ok"},
				}},
			},
			args: json.RawMessage(`{"query":"test"}`),
			assertRequest: func(t *testing.T, req ai.LLMGenerateRequest) {
				t.Helper()
				if req.Metadata["gemini.google_search"] != "true" {
					t.Fatalf("metadata = %v, want gemini.google_search=true", req.Metadata)
				}
			},
			wantResult: "ok",
		},
		{
			name: "request model and system prompt match config",
			cfg:  validSubAgentConfig(),
			provider: &providerStub{
				stream: &streamStub{chunks: []ai.LLMGenerateChunk{
					{Kind: ai.LLMGenerateChunkKindOutputText, Delta: "ok"},
				}},
			},
			args: json.RawMessage(`{"query":"test"}`),
			assertRequest: func(t *testing.T, req ai.LLMGenerateRequest) {
				t.Helper()
				if req.Model != "gemini-2.5-flash" {
					t.Fatalf("Model = %q, want gemini-2.5-flash", req.Model)
				}
				if len(req.Messages) < 2 {
					t.Fatalf("Messages len = %d, want >= 2", len(req.Messages))
				}
				if req.Messages[0].Role != ai.LLMMessageRoleSystem {
					t.Fatalf("Messages[0] role = %q, want system", req.Messages[0].Role)
				}
				if req.Messages[0].Content != "You are a search assistant." {
					t.Fatalf("Messages[0] content = %q, want system prompt", req.Messages[0].Content)
				}
				if req.Messages[1].Role != ai.LLMMessageRoleUser {
					t.Fatalf("Messages[1] role = %q, want user", req.Messages[1].Role)
				}
				if req.Messages[1].Content != "test" {
					t.Fatalf("Messages[1] content = %q, want rendered prompt", req.Messages[1].Content)
				}
			},
			wantResult: "ok",
		},
		{
			name: "nil context returns error",
			cfg:  validSubAgentConfig(),
			provider: &providerStub{
				stream: &streamStub{chunks: []ai.LLMGenerateChunk{
					{Kind: ai.LLMGenerateChunkKindOutputText, Delta: "ok"},
				}},
			},
			args:             json.RawMessage(`{"query":"test"}`),
			wantErrSubstring: "nil context",
		},
		{
			name: "empty response returns error",
			cfg:  validSubAgentConfig(),
			provider: &providerStub{
				stream: &streamStub{chunks: []ai.LLMGenerateChunk{}},
			},
			args:             json.RawMessage(`{"query":"test"}`),
			wantErrSubstring: "empty response",
		},
		{
			name: "provider stream error propagates",
			cfg:  validSubAgentConfig(),
			provider: &providerStub{
				streamErr: fmt.Errorf("api down"),
			},
			args:             json.RawMessage(`{"query":"test"}`),
			wantErrSubstring: "generate stream",
		},
		{
			name: "stream recv error propagates",
			cfg:  validSubAgentConfig(),
			provider: &providerStub{
				stream: &streamStub{recvErr: fmt.Errorf("network error")},
			},
			args:             json.RawMessage(`{"query":"test"}`),
			wantErrSubstring: "network error",
		},
		{
			name: "invalid args json returns error",
			cfg:  validSubAgentConfig(),
			provider: &providerStub{
				stream: &streamStub{chunks: []ai.LLMGenerateChunk{}},
			},
			args:             json.RawMessage(`not-json`),
			wantErrSubstring: "decode arguments",
		},
		{
			name: "template with conditional renders correctly",
			cfg: func() SubAgentConfig {
				cfg := validSubAgentConfig()
				cfg.PromptTemplate = "Read: {{.url}}{{if .question}} Q: {{.question}}{{end}}"
				cfg.Parameters = json.RawMessage(`{"type":"object","properties":{"url":{"type":"string"},"question":{"type":"string"}},"required":["url"]}`)
				return cfg
			}(),
			provider: &providerStub{
				stream: &streamStub{chunks: []ai.LLMGenerateChunk{
					{Kind: ai.LLMGenerateChunkKindOutputText, Delta: "content"},
				}},
			},
			args: json.RawMessage(`{"url":"https://example.com","question":"What is it?"}`),
			assertRequest: func(t *testing.T, req ai.LLMGenerateRequest) {
				t.Helper()
				want := "Read: https://example.com Q: What is it?"
				if req.Messages[1].Content != want {
					t.Fatalf("rendered prompt = %q, want %q", req.Messages[1].Content, want)
				}
			},
			wantResult: "content",
		},
		{
			name: "template with optional arg omitted renders without conditional block",
			cfg: func() SubAgentConfig {
				cfg := validSubAgentConfig()
				cfg.PromptTemplate = "Read: {{.url}}{{if .question}} Q: {{.question}}{{end}}"
				cfg.Parameters = json.RawMessage(`{"type":"object","properties":{"url":{"type":"string"},"question":{"type":"string"}},"required":["url"]}`)
				return cfg
			}(),
			provider: &providerStub{
				stream: &streamStub{chunks: []ai.LLMGenerateChunk{
					{Kind: ai.LLMGenerateChunkKindOutputText, Delta: "content"},
				}},
			},
			args: json.RawMessage(`{"url":"https://example.com"}`),
			assertRequest: func(t *testing.T, req ai.LLMGenerateRequest) {
				t.Helper()
				want := "Read: https://example.com"
				if req.Messages[1].Content != want {
					t.Fatalf("rendered prompt = %q, want %q", req.Messages[1].Content, want)
				}
			},
			wantResult: "content",
		},
		{
			name: "context cancellation propagates",
			cfg:  validSubAgentConfig(),
			provider: &providerStub{
				stream: &streamStub{recvErr: context.Canceled},
			},
			args:             json.RawMessage(`{"query":"test"}`),
			wantErrSubstring: "context canceled",
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			tool, err := newSubAgentTool(testCase.cfg, testCase.provider, nil)
			if err != nil {
				t.Fatalf("newSubAgentTool failed: %v", err)
			}

			ctx := context.Background()
			if testCase.name == "nil context returns error" {
				ctx = nil //nolint:staticcheck
			}

			result, execErr := tool.Execute(ctx, testCase.args)
			if testCase.wantErrSubstring != "" {
				if execErr == nil {
					t.Fatal("expected error")
				}
				if !containsSubstring(execErr.Error(), testCase.wantErrSubstring) {
					t.Fatalf("error = %v, want substring %q", execErr, testCase.wantErrSubstring)
				}
				return
			}
			if execErr != nil {
				t.Fatalf("Execute failed: %v", execErr)
			}

			if result != testCase.wantResult {
				t.Fatalf("result = %q, want %q", result, testCase.wantResult)
			}

			if testCase.assertRequest != nil {
				stub, ok := testCase.provider.(*providerStub)
				if !ok {
					t.Fatal("provider is not *providerStub")
				}
				testCase.assertRequest(t, stub.lastRequest)
			}
		})
	}
}

func TestSubAgentToolName(t *testing.T) {
	t.Parallel()

	tool, err := newSubAgentTool(validSubAgentConfig(), &providerStub{}, nil)
	if err != nil {
		t.Fatalf("newSubAgentTool failed: %v", err)
	}

	if tool.Name() != "web_search" {
		t.Fatalf("Name() = %q, want web_search", tool.Name())
	}
}

func TestSubAgentToolDefinition(t *testing.T) {
	t.Parallel()

	tool, err := newSubAgentTool(validSubAgentConfig(), &providerStub{}, nil)
	if err != nil {
		t.Fatalf("newSubAgentTool failed: %v", err)
	}

	def := tool.Definition()
	if def.Name != "web_search" {
		t.Fatalf("definition name = %q, want web_search", def.Name)
	}
	if def.Description != "Search the web" {
		t.Fatalf("definition description = %q, want 'Search the web'", def.Description)
	}
	if !json.Valid(def.Parameters) {
		t.Fatalf("definition parameters not valid json: %s", def.Parameters)
	}
}

func TestNewSubAgentToolValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		cfg              SubAgentConfig
		provider         ai.LLMProvider
		wantErrSubstring string
	}{
		{
			name:             "nil provider",
			cfg:              validSubAgentConfig(),
			provider:         nil,
			wantErrSubstring: "nil provider",
		},
		{
			name: "empty name",
			cfg: func() SubAgentConfig {
				cfg := validSubAgentConfig()
				cfg.Name = ""
				return cfg
			}(),
			provider:         &providerStub{},
			wantErrSubstring: "empty name",
		},
		{
			name: "empty prompt template",
			cfg: func() SubAgentConfig {
				cfg := validSubAgentConfig()
				cfg.PromptTemplate = ""
				return cfg
			}(),
			provider:         &providerStub{},
			wantErrSubstring: "empty prompt template",
		},
		{
			name: "invalid prompt template",
			cfg: func() SubAgentConfig {
				cfg := validSubAgentConfig()
				cfg.PromptTemplate = "{{.bad"
				return cfg
			}(),
			provider:         &providerStub{},
			wantErrSubstring: "parse prompt template",
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			_, err := newSubAgentTool(testCase.cfg, testCase.provider, nil)
			if err == nil {
				t.Fatal("expected error")
			}
			if !containsSubstring(err.Error(), testCase.wantErrSubstring) {
				t.Fatalf("error = %v, want substring %q", err, testCase.wantErrSubstring)
			}
		})
	}
}

func TestCollectStreamText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		stream           ai.LLMStream
		wantText         string
		wantErrSubstring string
	}{
		{
			name: "collects output text chunks",
			stream: &streamStub{chunks: []ai.LLMGenerateChunk{
				{Kind: ai.LLMGenerateChunkKindOutputText, Delta: "hello "},
				{Kind: ai.LLMGenerateChunkKindOutputText, Delta: "world"},
			}},
			wantText: "hello world",
		},
		{
			name: "skips thinking chunks",
			stream: &streamStub{chunks: []ai.LLMGenerateChunk{
				{Kind: ai.LLMGenerateChunkKindThinkingSummary, Delta: "thinking..."},
				{Kind: ai.LLMGenerateChunkKindOutputText, Delta: "answer"},
			}},
			wantText: "answer",
		},
		{
			name: "skips tool call chunks",
			stream: &streamStub{chunks: []ai.LLMGenerateChunk{
				{Kind: ai.LLMGenerateChunkKindToolCall, ToolCallID: "c1", ToolCallName: "fn"},
				{Kind: ai.LLMGenerateChunkKindOutputText, Delta: "text"},
			}},
			wantText: "text",
		},
		{
			name:     "empty stream returns empty string",
			stream:   &streamStub{chunks: []ai.LLMGenerateChunk{}},
			wantText: "",
		},
		{
			name:             "recv error propagates",
			stream:           &streamStub{recvErr: errors.New("broken")},
			wantErrSubstring: "broken",
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			result, err := collectStreamText(context.Background(), testCase.stream)
			if testCase.wantErrSubstring != "" {
				if err == nil {
					t.Fatal("expected error")
				}
				if !containsSubstring(err.Error(), testCase.wantErrSubstring) {
					t.Fatalf("error = %v, want substring %q", err, testCase.wantErrSubstring)
				}
				return
			}
			if err != nil {
				t.Fatalf("collectStreamText failed: %v", err)
			}
			if result != testCase.wantText {
				t.Fatalf("result = %q, want %q", result, testCase.wantText)
			}
		})
	}
}

func TestSubAgentToolStreamCloseError(t *testing.T) {
	t.Parallel()

	tool, err := newSubAgentTool(validSubAgentConfig(), &providerStub{
		stream: &streamStub{
			chunks:   []ai.LLMGenerateChunk{{Kind: ai.LLMGenerateChunkKindOutputText, Delta: "ok"}},
			closeErr: fmt.Errorf("close failed"),
		},
	}, nil)
	if err != nil {
		t.Fatalf("newSubAgentTool failed: %v", err)
	}

	_, execErr := tool.Execute(context.Background(), json.RawMessage(`{"query":"test"}`))
	if execErr == nil {
		t.Fatal("expected error from stream close")
	}
	if !containsSubstring(execErr.Error(), "close stream") {
		t.Fatalf("error = %v, want substring 'close stream'", execErr)
	}
}

func TestSubAgentToolRecvAndCloseErrors(t *testing.T) {
	t.Parallel()

	tool, err := newSubAgentTool(validSubAgentConfig(), &providerStub{
		stream: &streamStub{
			recvErr:  fmt.Errorf("recv failed"),
			closeErr: fmt.Errorf("close also failed"),
		},
	}, nil)
	if err != nil {
		t.Fatalf("newSubAgentTool failed: %v", err)
	}

	_, execErr := tool.Execute(context.Background(), json.RawMessage(`{"query":"test"}`))
	if execErr == nil {
		t.Fatal("expected error")
	}
	if !containsSubstring(execErr.Error(), "recv failed") {
		t.Fatalf("error should contain recv error: %v", execErr)
	}
	if !containsSubstring(execErr.Error(), "close") {
		t.Fatalf("error should contain close error: %v", execErr)
	}
}

func validSubAgentConfig() SubAgentConfig {
	return SubAgentConfig{
		Name:            "web_search",
		Description:     "Search the web",
		Provider:        "gemini-main",
		Model:           "gemini-2.5-flash",
		SystemPrompt:    "You are a search assistant.",
		MaxOutputTokens: 4096,
		Temperature:     0.3,
		Parameters:      json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false}`),
		PromptTemplate:  "{{.query}}",
	}
}

func TestStreamProviderReplyWithSubAgentToolCall(t *testing.T) {
	t.Parallel()

	module := newTestModule(validModuleConfig())
	sink := &sinkDispatcherStub{}
	module.dispatcher = sink
	module.clock = func() time.Time { return time.Unix(0, 0).UTC() }

	// Sub-agent provider: returns search results when called.
	subAgentProvider := &providerStub{
		stream: &streamStub{chunks: []ai.LLMGenerateChunk{
			{Kind: ai.LLMGenerateChunkKindOutputText, Delta: "Go 1.22 introduced "},
			{Kind: ai.LLMGenerateChunkKindOutputText, Delta: "range over integers."},
		}},
	}

	subAgentCfg := validSubAgentConfig()
	tool, err := newSubAgentTool(subAgentCfg, subAgentProvider, nil)
	if err != nil {
		t.Fatalf("newSubAgentTool failed: %v", err)
	}
	registry := NewToolRegistry([]ToolHandler{tool})

	// Main provider: first call returns a tool call for web_search,
	// second call returns the final answer incorporating the search result.
	mainProvider := &providerStub{
		streams: []ai.LLMStream{
			&streamStub{chunks: []ai.LLMGenerateChunk{
				{Kind: ai.LLMGenerateChunkKindToolCall, ToolCallID: "call-1", ToolCallName: "web_search"},
				{Kind: ai.LLMGenerateChunkKindToolCall, ToolCallID: "call-1", ToolCallArguments: `{"query":"Go 1.22 features"}`},
			}},
			&streamStub{chunks: []ai.LLMGenerateChunk{
				{Kind: ai.LLMGenerateChunkKindOutputText, Delta: "According to my search, Go 1.22 introduced range over integers."},
			}},
		},
	}

	req := ai.LLMGenerateRequest{
		Model: "gpt-test",
		Messages: []ai.LLMMessage{
			{Role: ai.LLMMessageRoleSystem, Content: "sys"},
			{Role: ai.LLMMessageRoleUser, Content: "What's new in Go 1.22?"},
		},
		Tools: registry.Definitions(),
	}

	if err := module.streamProviderReplyWithTools(
		context.Background(),
		context.Background(),
		platform.OutboundTarget{Conversation: platform.Conversation{ID: "chat-1", Type: platform.ConversationTypeGroup}},
		"placeholder-1",
		mainProvider,
		req,
		registry,
	); err != nil {
		t.Fatalf("streamProviderReplyWithTools failed: %v", err)
	}

	// Main provider should be called twice.
	if len(mainProvider.requests) != 2 {
		t.Fatalf("main provider request count = %d, want 2", len(mainProvider.requests))
	}

	// Sub-agent provider should be called once.
	if len(subAgentProvider.requests) != 1 {
		t.Fatalf("sub-agent provider request count = %d, want 1", len(subAgentProvider.requests))
	}

	// Sub-agent request should have no function tools.
	subReq := subAgentProvider.requests[0]
	if subReq.Tools != nil {
		t.Fatalf("sub-agent request Tools = %v, want nil", subReq.Tools)
	}
	if subReq.Model != "gemini-2.5-flash" {
		t.Fatalf("sub-agent request Model = %q, want gemini-2.5-flash", subReq.Model)
	}

	// Follow-up request should contain tool result from sub-agent.
	followUp := mainProvider.requests[1]
	if len(followUp.Messages) != 4 {
		t.Fatalf("follow-up message count = %d, want 4", len(followUp.Messages))
	}

	assistantMsg := followUp.Messages[2]
	if assistantMsg.Role != ai.LLMMessageRoleAssistant {
		t.Fatalf("assistant role = %q, want assistant", assistantMsg.Role)
	}
	if len(assistantMsg.ToolCalls) != 1 || assistantMsg.ToolCalls[0].Name != "web_search" {
		t.Fatalf("assistant tool call = %+v, want web_search", assistantMsg.ToolCalls)
	}

	toolMsg := followUp.Messages[3]
	if toolMsg.Role != ai.LLMMessageRoleTool {
		t.Fatalf("tool message role = %q, want tool", toolMsg.Role)
	}
	if toolMsg.ToolCallID != "call-1" {
		t.Fatalf("tool message tool_call_id = %q, want call-1", toolMsg.ToolCallID)
	}
	if toolMsg.Content != "Go 1.22 introduced range over integers." {
		t.Fatalf("tool message content = %q, want sub-agent result", toolMsg.Content)
	}

	// Final edit should contain the main agent's response.
	if len(sink.editRequests) < 1 {
		t.Fatal("expected at least 1 edit request")
	}
	lastEdit := sink.editRequests[len(sink.editRequests)-1]
	if lastEdit.Text != "According to my search, Go 1.22 introduced range over integers." {
		t.Fatalf("final edit text = %q, want main agent response", lastEdit.Text)
	}
}

func TestBuildToolRegistryMemoryAndSubAgents(t *testing.T) {
	t.Parallel()

	module := newTestModule(Config{
		RequestTimeout: 30 * 1e9,
		Agents: []Agent{{
			Name:                 "Otogi",
			Description:          "d",
			Provider:             "gemini",
			Model:                "m",
			SystemPromptTemplate: "ok",
			RequestTimeout:       10 * 1e9,
			SubAgents: []SubAgentConfig{
				validSubAgentConfig(),
			},
		}},
	})
	module.providers = map[string]ai.LLMProvider{
		"gemini":      &providerStub{},
		"gemini-main": &providerStub{},
	}

	event := testLLMChatEvent("test")
	registry, err := module.buildToolRegistry(event, module.cfg.Agents[0])
	if err != nil {
		t.Fatalf("buildToolRegistry failed: %v", err)
	}
	if registry == nil {
		t.Fatal("expected non-nil registry")
	}

	defs := registry.Definitions()
	if len(defs) != 1 {
		t.Fatalf("definitions len = %d, want 1 (sub-agent only, no memory)", len(defs))
	}
	if defs[0].Name != "web_search" {
		t.Fatalf("definition name = %q, want web_search", defs[0].Name)
	}
}

func TestBuildToolRegistryNoToolsReturnsNil(t *testing.T) {
	t.Parallel()

	module := newTestModule(Config{
		RequestTimeout: 30 * 1e9,
		Agents: []Agent{{
			Name:                 "Otogi",
			Description:          "d",
			Provider:             "gemini",
			Model:                "m",
			SystemPromptTemplate: "ok",
			RequestTimeout:       10 * 1e9,
		}},
	})
	module.providers = map[string]ai.LLMProvider{
		"gemini": &providerStub{},
	}

	event := testLLMChatEvent("test")
	registry, err := module.buildToolRegistry(event, module.cfg.Agents[0])
	if err != nil {
		t.Fatalf("buildToolRegistry failed: %v", err)
	}
	if registry != nil {
		t.Fatal("expected nil registry when no tools configured")
	}
}

func TestBuildToolRegistrySubAgentMissingProvider(t *testing.T) {
	t.Parallel()

	module := newTestModule(Config{
		RequestTimeout: 30 * 1e9,
		Agents: []Agent{{
			Name:                 "Otogi",
			Description:          "d",
			Provider:             "gemini",
			Model:                "m",
			SystemPromptTemplate: "ok",
			RequestTimeout:       10 * 1e9,
			SubAgents: []SubAgentConfig{
				validSubAgentConfig(),
			},
		}},
	})
	module.providers = map[string]ai.LLMProvider{
		"gemini": &providerStub{},
		// "gemini-main" is NOT in providers map
	}

	event := testLLMChatEvent("test")
	_, err := module.buildToolRegistry(event, module.cfg.Agents[0])
	if err == nil {
		t.Fatal("expected error for missing sub-agent provider")
	}
	if !containsSubstring(err.Error(), "not available") {
		t.Fatalf("error = %v, want substring 'not available'", err)
	}
}

func containsSubstring(s string, substr string) bool {
	return len(s) >= len(substr) && (substr == "" || indexOf(s, substr) >= 0)
}

func indexOf(s string, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

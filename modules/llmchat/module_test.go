package llmchat

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"ex-otogi/pkg/otogi"
)

func TestSpecSetsExplicitHandlerTimeout(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		requestTimeout time.Duration
	}{
		{name: "short timeout", requestTimeout: 15 * time.Second},
		{name: "long timeout", requestTimeout: 2 * time.Minute},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			module, err := New(Config{
				RequestTimeout: testCase.requestTimeout,
				Agents: []Agent{
					{
						Name:                 "Otogi",
						Description:          "assistant",
						Provider:             "p",
						Model:                "m",
						SystemPromptTemplate: "You are {{.AgentName}}",
						RequestTimeout:       time.Second,
					},
				},
			})
			if err != nil {
				t.Fatalf("New failed: %v", err)
			}

			spec := module.Spec()
			if len(spec.Handlers) != 1 {
				t.Fatalf("handlers len = %d, want 1", len(spec.Handlers))
			}

			subscription := spec.Handlers[0].Subscription
			if subscription.Name != "llmchat-articles" {
				t.Fatalf("subscription name = %q, want llmchat-articles", subscription.Name)
			}

			wantTimeout := testCase.requestTimeout + llmchatHandlerTimeoutGrace
			if subscription.HandlerTimeout != wantTimeout {
				t.Fatalf("handler timeout = %s, want %s", subscription.HandlerTimeout, wantTimeout)
			}
		})
	}
}

func TestMatchTriggeredAgent(t *testing.T) {
	module, err := New(Config{
		RequestTimeout: time.Second,
		Agents: []Agent{
			{
				Name:                 "Otogi",
				Description:          "primary",
				Provider:             "p",
				Model:                "m",
				SystemPromptTemplate: "You are {{.AgentName}}",
				RequestTimeout:       time.Second,
			},
			{
				Name:                 "Ai",
				Description:          "short",
				Provider:             "p",
				Model:                "m",
				SystemPromptTemplate: "You are {{.AgentName}}",
				RequestTimeout:       time.Second,
			},
		},
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	tests := []struct {
		name      string
		text      string
		wantMatch bool
		wantAgent string
		wantBody  string
	}{
		{name: "exact prefix", text: "Otogi hello", wantMatch: true, wantAgent: "Otogi", wantBody: "hello"},
		{name: "colon separator", text: "Otogi: hello", wantMatch: true, wantAgent: "Otogi", wantBody: "hello"},
		{name: "punct separator", text: "Otogi, hello", wantMatch: true, wantAgent: "Otogi", wantBody: "hello"},
		{name: "no body", text: "Otogi", wantMatch: true, wantAgent: "Otogi", wantBody: ""},
		{name: "not prefix", text: "hello Otogi", wantMatch: false},
		{name: "word continuation rejects", text: "Otogix hi", wantMatch: false},
		{name: "case-insensitive", text: "otogi hi", wantMatch: true, wantAgent: "Otogi", wantBody: "hi"},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			agent, body, matched := module.matchTriggeredAgent(testCase.text)
			if matched != testCase.wantMatch {
				t.Fatalf("matched = %v, want %v", matched, testCase.wantMatch)
			}
			if !matched {
				return
			}
			if agent.Name != testCase.wantAgent {
				t.Fatalf("agent = %q, want %q", agent.Name, testCase.wantAgent)
			}
			if body != testCase.wantBody {
				t.Fatalf("body = %q, want %q", body, testCase.wantBody)
			}
		})
	}
}

func TestBuildGenerateRequestUsesReplyChainAndSpeakerNames(t *testing.T) {
	module, err := New(Config{
		RequestTimeout: time.Second,
		Agents: []Agent{
			{
				Name:                 "Otogi",
				Description:          "assistant",
				Provider:             "p",
				Model:                "gpt-test",
				SystemPromptTemplate: "You are {{.AgentName}} at {{.DateTimeUTC}}",
				RequestTimeout:       time.Second,
				RequestMetadata: map[string]string{
					"gemini.google_search":      "true",
					"gemini.url_context":        "false",
					"gemini.thinking_budget":    "32",
					"gemini.include_thoughts":   "true",
					"gemini.thinking_level":     "medium",
					"gemini.response_mime_type": "application/json",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	module.memory = &memoryStub{
		replyChain: []otogi.ReplyChainEntry{
			{
				Conversation: otogi.Conversation{ID: "chat-1", Type: otogi.ConversationTypeGroup},
				Actor:        otogi.Actor{ID: "u1", Username: "alice"},
				Article:      otogi.Article{ID: "m1", Text: "hello"},
			},
			{
				Conversation: otogi.Conversation{ID: "chat-1", Type: otogi.ConversationTypeGroup},
				Actor:        otogi.Actor{ID: "u2", DisplayName: "Bob"},
				Article:      otogi.Article{ID: "m2", Text: "trigger text"},
				IsCurrent:    true,
			},
		},
	}
	module.clock = func() time.Time { return time.Unix(100, 0).UTC() }

	req, err := module.buildGenerateRequest(context.Background(), &otogi.Event{
		ID:         "evt-1",
		Kind:       otogi.EventKindArticleCreated,
		OccurredAt: time.Unix(100, 0).UTC(),
		Source: otogi.EventSource{
			Platform: otogi.PlatformTelegram,
			ID:       "tg-main",
		},
		Conversation: otogi.Conversation{ID: "chat-1", Type: otogi.ConversationTypeGroup},
		Actor:        otogi.Actor{ID: "u2", DisplayName: "Bob"},
		Article: &otogi.Article{
			ID:               "m2",
			ReplyToArticleID: "m1",
			Text:             "Otogi hi",
		},
	}, module.cfg.Agents[0], "how are you")
	if err != nil {
		t.Fatalf("buildGenerateRequest failed: %v", err)
	}
	if len(req.Messages) < 3 {
		t.Fatalf("messages len = %d, want >= 3", len(req.Messages))
	}
	if req.Messages[0].Role != otogi.LLMMessageRoleSystem {
		t.Fatalf("system role = %q, want %q", req.Messages[0].Role, otogi.LLMMessageRoleSystem)
	}
	if !strings.Contains(req.Messages[1].Content, "alice: hello") {
		t.Fatalf("message[1] = %q, want alice speaker format", req.Messages[1].Content)
	}
	if !strings.Contains(req.Messages[2].Content, "Bob: how are you") {
		t.Fatalf("message[2] = %q, want current speaker with stripped prompt", req.Messages[2].Content)
	}
	if req.Metadata[metadataKeyAgent] != "Otogi" {
		t.Fatalf("metadata[%s] = %q, want Otogi", metadataKeyAgent, req.Metadata[metadataKeyAgent])
	}
	if req.Metadata[metadataKeyProvider] != "p" {
		t.Fatalf("metadata[%s] = %q, want p", metadataKeyProvider, req.Metadata[metadataKeyProvider])
	}
	if req.Metadata[metadataKeyConversationID] != "chat-1" {
		t.Fatalf(
			"metadata[%s] = %q, want chat-1",
			metadataKeyConversationID,
			req.Metadata[metadataKeyConversationID],
		)
	}
	if req.Metadata["gemini.google_search"] != "true" {
		t.Fatalf("metadata[gemini.google_search] = %q, want true", req.Metadata["gemini.google_search"])
	}
	if req.Metadata["gemini.url_context"] != "false" {
		t.Fatalf("metadata[gemini.url_context] = %q, want false", req.Metadata["gemini.url_context"])
	}
	if req.Metadata["gemini.thinking_budget"] != "32" {
		t.Fatalf("metadata[gemini.thinking_budget] = %q, want 32", req.Metadata["gemini.thinking_budget"])
	}
	if req.Metadata["gemini.include_thoughts"] != "true" {
		t.Fatalf("metadata[gemini.include_thoughts] = %q, want true", req.Metadata["gemini.include_thoughts"])
	}
	if req.Metadata["gemini.thinking_level"] != "medium" {
		t.Fatalf("metadata[gemini.thinking_level] = %q, want medium", req.Metadata["gemini.thinking_level"])
	}
	if req.Metadata["gemini.response_mime_type"] != "application/json" {
		t.Fatalf(
			"metadata[gemini.response_mime_type] = %q, want application/json",
			req.Metadata["gemini.response_mime_type"],
		)
	}
}

func TestOnRegisterResolvesProviders(t *testing.T) {
	module, err := New(Config{
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
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	provider := &providerStub{}
	registry := &providerRegistryStub{providers: map[string]otogi.LLMProvider{"openai": provider}}
	runtime := moduleRuntimeStub{registry: serviceRegistryStub{values: map[string]any{
		otogi.ServiceSinkDispatcher:      &sinkDispatcherStub{},
		otogi.ServiceMemory:              &memoryStub{},
		otogi.ServiceLLMProviderRegistry: registry,
	}}}

	if err := module.OnRegister(context.Background(), runtime); err != nil {
		t.Fatalf("OnRegister failed: %v", err)
	}
	if module.providers["openai"] == nil {
		t.Fatal("expected openai provider to be resolved")
	}
}

func TestEditPacerBaseIntervals(t *testing.T) {
	t.Parallel()

	start := time.Unix(0, 0).UTC()
	pacer := newEditPacer(start)

	cases := []struct {
		at   time.Duration
		want time.Duration
	}{
		{at: 500 * time.Millisecond, want: 2 * time.Second},
		{at: 8 * time.Second, want: 3500 * time.Millisecond},
		{at: 30 * time.Second, want: 6 * time.Second},
		{at: 80 * time.Second, want: 10 * time.Second},
	}
	for _, testCase := range cases {
		got := pacer.baseInterval(start.Add(testCase.at))
		if got != testCase.want {
			t.Fatalf("base interval at %s = %s, want %s", testCase.at, got, testCase.want)
		}
	}
}

func TestEditPacerBackoffAndRateLimit(t *testing.T) {
	t.Parallel()

	start := time.Unix(0, 0).UTC()
	pacer := newEditPacer(start)

	pacer.RecordEditFailure(start.Add(time.Second), errors.New("temporary failure"))
	first := pacer.currentInterval
	if first <= 2*time.Second {
		t.Fatalf("first interval = %s, want > 2s", first)
	}

	pacer.RecordEditFailure(start.Add(2*time.Second), errors.New("temporary failure"))
	second := pacer.currentInterval
	if second <= first {
		t.Fatalf("second interval = %s, want > first interval %s", second, first)
	}

	pacer.RecordEditFailure(start.Add(3*time.Second), errors.New("429 too many requests"))
	if pacer.currentInterval != maxEditInterval {
		t.Fatalf("rate-limit interval = %s, want %s", pacer.currentInterval, maxEditInterval)
	}
}

func TestStreamProviderReplyPausesIntermediateEditsAfterFailures(t *testing.T) {
	module, err := New(Config{
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
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	sink := &sinkDispatcherStub{
		editErrors: []error{
			errors.New("edit fail 1"),
			errors.New("edit fail 2"),
			errors.New("edit fail 3"),
			nil,
		},
	}
	module.dispatcher = sink
	clock := sequenceClock([]time.Time{
		time.Unix(0, 0).UTC(),
		time.Unix(0, 0).UTC(),
		time.Unix(4, 0).UTC(),
		time.Unix(12, 0).UTC(),
		time.Unix(22, 0).UTC(),
	})
	module.clock = clock

	provider := &providerStub{stream: &streamStub{chunks: []otogi.LLMGenerateChunk{
		{Delta: "a"},
		{Delta: "b"},
		{Delta: "c"},
		{Delta: "d"},
	}}}
	req := otogi.LLMGenerateRequest{
		Model: "gpt-test",
		Messages: []otogi.LLMMessage{
			{Role: otogi.LLMMessageRoleSystem, Content: "sys"},
			{Role: otogi.LLMMessageRoleUser, Content: "u"},
		},
	}
	if err := module.streamProviderReply(
		context.Background(),
		otogi.OutboundTarget{Conversation: otogi.Conversation{ID: "chat-1", Type: otogi.ConversationTypeGroup}},
		"src-1",
		"placeholder-1",
		provider,
		req,
	); err != nil {
		t.Fatalf("streamProviderReply failed: %v", err)
	}

	if len(sink.editRequests) != 4 {
		t.Fatalf("edit request count = %d, want 4 (3 failures + final)", len(sink.editRequests))
	}
	if sink.editRequests[len(sink.editRequests)-1].Text != "abcd" {
		t.Fatalf("final edit text = %q, want abcd", sink.editRequests[len(sink.editRequests)-1].Text)
	}
}

func TestStreamProviderReplyFinalEditFallbackSend(t *testing.T) {
	module, err := New(Config{
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
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	sink := &sinkDispatcherStub{
		editErrors: []error{nil, errors.New("final edit failed")},
	}
	module.dispatcher = sink
	module.clock = sequenceClock([]time.Time{
		time.Unix(0, 0).UTC(),
		time.Unix(0, 0).UTC(),
		time.Unix(0, 0).UTC(),
	})

	provider := &providerStub{stream: &streamStub{chunks: []otogi.LLMGenerateChunk{
		{Delta: "hello"},
		{Delta: " world"},
	}}}
	req := otogi.LLMGenerateRequest{
		Model: "gpt-test",
		Messages: []otogi.LLMMessage{
			{Role: otogi.LLMMessageRoleSystem, Content: "sys"},
			{Role: otogi.LLMMessageRoleUser, Content: "u"},
		},
	}
	if err := module.streamProviderReply(
		context.Background(),
		otogi.OutboundTarget{Conversation: otogi.Conversation{ID: "chat-1", Type: otogi.ConversationTypeGroup}},
		"src-1",
		"placeholder-1",
		provider,
		req,
	); err != nil {
		t.Fatalf("streamProviderReply failed: %v", err)
	}

	if len(sink.sendRequests) != 1 {
		t.Fatalf("send request count = %d, want 1 fallback", len(sink.sendRequests))
	}
	if sink.sendRequests[0].Text != "hello world" {
		t.Fatalf("fallback text = %q, want hello world", sink.sendRequests[0].Text)
	}
	if sink.sendRequests[0].ReplyToMessageID != "src-1" {
		t.Fatalf("fallback reply_to_message_id = %q, want src-1", sink.sendRequests[0].ReplyToMessageID)
	}
}

func TestStreamProviderReplySkipsFinalEditWhenFinalTextAlreadyDelivered(t *testing.T) {
	module, err := New(Config{
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
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	sink := &sinkDispatcherStub{
		editErrors: []error{nil, errors.New("unexpected final edit")},
	}
	module.dispatcher = sink
	module.clock = sequenceClock([]time.Time{
		time.Unix(0, 0).UTC(),
		time.Unix(0, 0).UTC(),
	})

	provider := &providerStub{stream: &streamStub{chunks: []otogi.LLMGenerateChunk{{Delta: "hello"}}}}
	req := otogi.LLMGenerateRequest{
		Model: "gpt-test",
		Messages: []otogi.LLMMessage{
			{Role: otogi.LLMMessageRoleSystem, Content: "sys"},
			{Role: otogi.LLMMessageRoleUser, Content: "u"},
		},
	}
	if err := module.streamProviderReply(
		context.Background(),
		otogi.OutboundTarget{Conversation: otogi.Conversation{ID: "chat-1", Type: otogi.ConversationTypeGroup}},
		"src-1",
		"placeholder-1",
		provider,
		req,
	); err != nil {
		t.Fatalf("streamProviderReply failed: %v", err)
	}

	if len(sink.editRequests) != 1 {
		t.Fatalf("edit request count = %d, want 1", len(sink.editRequests))
	}
	if sink.editRequests[0].Text != "hello" {
		t.Fatalf("edit text = %q, want hello", sink.editRequests[0].Text)
	}
	if len(sink.sendRequests) != 0 {
		t.Fatalf("send request count = %d, want 0", len(sink.sendRequests))
	}
}

func TestStreamProviderReplyShowsThinkingThenFinalAnswer(t *testing.T) {
	module, err := New(Config{
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
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	sink := &sinkDispatcherStub{}
	module.dispatcher = sink
	module.clock = sequenceClock([]time.Time{
		time.Unix(0, 0).UTC(),
		time.Unix(0, 0).UTC(),
		time.Unix(3, 0).UTC(),
	})

	provider := &providerStub{stream: &streamStub{chunks: []otogi.LLMGenerateChunk{
		{Kind: otogi.LLMGenerateChunkKindThinkingSummary, Delta: "  Plan\nsteps\t"},
		{Kind: otogi.LLMGenerateChunkKindOutputText, Delta: "final answer"},
	}}}
	req := otogi.LLMGenerateRequest{
		Model: "gpt-test",
		Messages: []otogi.LLMMessage{
			{Role: otogi.LLMMessageRoleSystem, Content: "sys"},
			{Role: otogi.LLMMessageRoleUser, Content: "u"},
		},
	}
	if err := module.streamProviderReply(
		context.Background(),
		otogi.OutboundTarget{Conversation: otogi.Conversation{ID: "chat-1", Type: otogi.ConversationTypeGroup}},
		"src-1",
		"placeholder-1",
		provider,
		req,
	); err != nil {
		t.Fatalf("streamProviderReply failed: %v", err)
	}

	if len(sink.sendRequests) != 0 {
		t.Fatalf("send request count = %d, want 0", len(sink.sendRequests))
	}
	if len(sink.editRequests) < 2 {
		t.Fatalf("edit request count = %d, want at least 2", len(sink.editRequests))
	}
	if sink.editRequests[0].Text != "Thinking...\n\nPlan steps" {
		t.Fatalf("first edit text = %q, want thinking preview", sink.editRequests[0].Text)
	}

	finalText := sink.editRequests[len(sink.editRequests)-1].Text
	if finalText != "final answer" {
		t.Fatalf("final edit text = %q, want final answer", finalText)
	}
	if strings.Contains(strings.ToLower(finalText), "thinking") {
		t.Fatalf("final edit should not contain thinking text, got %q", finalText)
	}
}

func TestStreamProviderReplyThinkingOnlyReturnsError(t *testing.T) {
	module, err := New(Config{
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
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	sink := &sinkDispatcherStub{}
	module.dispatcher = sink
	module.clock = sequenceClock([]time.Time{
		time.Unix(0, 0).UTC(),
		time.Unix(0, 0).UTC(),
	})

	longSummary := strings.Repeat("summary ", 80)
	provider := &providerStub{stream: &streamStub{chunks: []otogi.LLMGenerateChunk{
		{Kind: otogi.LLMGenerateChunkKindThinkingSummary, Delta: longSummary},
	}}}
	req := otogi.LLMGenerateRequest{
		Model: "gpt-test",
		Messages: []otogi.LLMMessage{
			{Role: otogi.LLMMessageRoleSystem, Content: "sys"},
			{Role: otogi.LLMMessageRoleUser, Content: "u"},
		},
	}
	err = module.streamProviderReply(
		context.Background(),
		otogi.OutboundTarget{Conversation: otogi.Conversation{ID: "chat-1", Type: otogi.ConversationTypeGroup}},
		"src-1",
		"placeholder-1",
		provider,
		req,
	)
	if err == nil {
		t.Fatal("streamProviderReply error = nil, want thinking-only error")
	}
	if !strings.Contains(err.Error(), "no output text received") {
		t.Fatalf("error = %q, want no output text received", err)
	}
	if !strings.Contains(err.Error(), "thinking_chunks=1") {
		t.Fatalf("error = %q, want thinking chunk diagnostics", err)
	}
	if !strings.Contains(err.Error(), "output_chunks=0") {
		t.Fatalf("error = %q, want output chunk diagnostics", err)
	}
	if !strings.Contains(err.Error(), "deadline_remaining=none") {
		t.Fatalf("error = %q, want deadline remaining diagnostics", err)
	}

	if len(sink.editRequests) == 0 {
		t.Fatal("expected edit requests")
	}
	if len(sink.sendRequests) != 0 {
		t.Fatalf("send request count = %d, want 0", len(sink.sendRequests))
	}

	thinkingText := sink.editRequests[len(sink.editRequests)-1].Text
	prefix := defaultThinkingPlaceholder + "\n\n"
	if !strings.HasPrefix(thinkingText, prefix) {
		t.Fatalf("thinking text prefix = %q, want %q", thinkingText, prefix)
	}
	preview := strings.TrimPrefix(thinkingText, prefix)
	if len([]rune(preview)) != maxThinkingPreviewRunes {
		t.Fatalf("preview rune length = %d, want %d", len([]rune(preview)), maxThinkingPreviewRunes)
	}
	if !strings.HasSuffix(preview, "...") {
		t.Fatalf("preview should be truncated with ellipsis, got %q", preview)
	}
}

func TestStreamProviderReplyNoOutputReturnsContextErrorWhenCanceled(t *testing.T) {
	module, err := New(Config{
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
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	sink := &sinkDispatcherStub{}
	module.dispatcher = sink

	req := otogi.LLMGenerateRequest{
		Model: "gpt-test",
		Messages: []otogi.LLMMessage{
			{Role: otogi.LLMMessageRoleSystem, Content: "sys"},
			{Role: otogi.LLMMessageRoleUser, Content: "u"},
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = module.streamProviderReply(
		ctx,
		otogi.OutboundTarget{Conversation: otogi.Conversation{ID: "chat-1", Type: otogi.ConversationTypeGroup}},
		"src-1",
		"placeholder-1",
		&providerStub{stream: &streamStub{}},
		req,
	)
	if err == nil {
		t.Fatal("streamProviderReply error = nil, want context cancellation error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", err)
	}
	if strings.Contains(err.Error(), "no output text received") {
		t.Fatalf("error = %q, should not map canceled stream to no output text", err)
	}
}

func TestShapeThinkingSummaryNormalizesWhitespace(t *testing.T) {
	t.Parallel()

	shaped := shapeThinkingSummary("  a\tb\nc  ")
	if shaped != "a b c" {
		t.Fatalf("shapeThinkingSummary = %q, want %q", shaped, "a b c")
	}
}

func TestStreamProviderReplyRequiresPlaceholderMessageID(t *testing.T) {
	module, err := New(Config{
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
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	req := otogi.LLMGenerateRequest{
		Model: "gpt-test",
		Messages: []otogi.LLMMessage{
			{Role: otogi.LLMMessageRoleSystem, Content: "sys"},
			{Role: otogi.LLMMessageRoleUser, Content: "u"},
		},
	}
	err = module.streamProviderReply(
		context.Background(),
		otogi.OutboundTarget{Conversation: otogi.Conversation{ID: "chat-1", Type: otogi.ConversationTypeGroup}},
		"src-1",
		"",
		&providerStub{stream: &streamStub{chunks: []otogi.LLMGenerateChunk{{Delta: "hello"}}}},
		req,
	)
	if err == nil {
		t.Fatal("streamProviderReply error = nil, want placeholder validation error")
	}
	if !strings.Contains(err.Error(), "empty placeholder message id") {
		t.Fatalf("error = %q, want empty placeholder message id", err)
	}
}

func TestHandleArticleIgnoresBotMessages(t *testing.T) {
	module, err := New(Config{
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
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	module.dispatcher = &sinkDispatcherStub{}
	module.memory = &memoryStub{}
	module.providers = map[string]otogi.LLMProvider{
		"openai": &providerStub{stream: &streamStub{chunks: []otogi.LLMGenerateChunk{{Delta: "hello"}}}},
	}

	err = module.handleArticle(context.Background(), &otogi.Event{
		Kind:       otogi.EventKindArticleCreated,
		OccurredAt: time.Unix(1, 0).UTC(),
		Source:     otogi.EventSource{Platform: otogi.PlatformTelegram, ID: "tg-main"},
		Conversation: otogi.Conversation{
			ID:   "chat-1",
			Type: otogi.ConversationTypeGroup,
		},
		Actor:   otogi.Actor{ID: "bot-1", IsBot: true},
		Article: &otogi.Article{ID: "m-1", Text: "Otogi hello"},
	})
	if err != nil {
		t.Fatalf("handleArticle failed: %v", err)
	}
}

func TestHandleArticleSendsPlaceholderBeforeBuildAndStream(t *testing.T) {
	module, err := New(Config{
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
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	callOrder := make([]string, 0, 3)
	sink := &sinkDispatcherStub{
		onSend: func(otogi.SendMessageRequest) {
			callOrder = append(callOrder, "send")
		},
	}
	memory := &memoryStub{
		replyChain: []otogi.ReplyChainEntry{
			{
				Conversation: otogi.Conversation{ID: "chat-1", Type: otogi.ConversationTypeGroup},
				Actor:        otogi.Actor{ID: "user-1", DisplayName: "Alice"},
				Article:      otogi.Article{ID: "m-1", Text: "Otogi hello"},
				IsCurrent:    true,
			},
		},
		onGetReplyChain: func() {
			callOrder = append(callOrder, "memory")
		},
	}
	provider := &providerStub{
		stream: &streamStub{chunks: []otogi.LLMGenerateChunk{
			{Delta: "hello"},
		}},
		onGenerateStream: func() {
			callOrder = append(callOrder, "provider")
		},
	}
	module.dispatcher = sink
	module.memory = memory
	module.providers = map[string]otogi.LLMProvider{"openai": provider}
	module.clock = sequenceClock([]time.Time{
		time.Unix(0, 0).UTC(),
		time.Unix(2, 0).UTC(),
	})

	event := testLLMChatEvent("Otogi hello")
	if err := module.handleArticle(context.Background(), event); err != nil {
		t.Fatalf("handleArticle failed: %v", err)
	}

	if len(sink.sendRequests) != 1 {
		t.Fatalf("send request count = %d, want 1", len(sink.sendRequests))
	}
	if sink.sendRequests[0].Text != defaultThinkingPlaceholder {
		t.Fatalf("placeholder text = %q, want %q", sink.sendRequests[0].Text, defaultThinkingPlaceholder)
	}
	if sink.sendRequests[0].ReplyToMessageID != event.Article.ID {
		t.Fatalf("placeholder reply_to_message_id = %q, want %q", sink.sendRequests[0].ReplyToMessageID, event.Article.ID)
	}

	if got, want := strings.Join(callOrder, ","), "send,memory,provider"; got != want {
		t.Fatalf("call order = %q, want %q", got, want)
	}
}

func TestHandleArticleDeadlinePreflightFailureEditsPlaceholder(t *testing.T) {
	module, err := New(Config{
		RequestTimeout: 90 * time.Second,
		Agents: []Agent{
			{
				Name:                 "Otogi",
				Description:          "assistant",
				Provider:             "openai",
				Model:                "gpt-test",
				SystemPromptTemplate: "You are {{.AgentName}}",
				RequestTimeout:       90 * time.Second,
			},
		},
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	providerCalled := false
	sink := &sinkDispatcherStub{}
	module.dispatcher = sink
	module.memory = &memoryStub{
		replyChain: []otogi.ReplyChainEntry{
			{
				Conversation: otogi.Conversation{ID: "chat-1", Type: otogi.ConversationTypeGroup},
				Actor:        otogi.Actor{ID: "user-1", DisplayName: "Alice"},
				Article:      otogi.Article{ID: "m-1", Text: "Otogi hello"},
				IsCurrent:    true,
			},
		},
	}
	module.providers = map[string]otogi.LLMProvider{
		"openai": &providerStub{
			stream: &streamStub{chunks: []otogi.LLMGenerateChunk{{Delta: "unused"}}},
			onGenerateStream: func() {
				providerCalled = true
			},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err = module.handleArticle(ctx, testLLMChatEvent("Otogi hello"))
	if err == nil {
		t.Fatal("handleArticle error = nil, want deadline preflight failure")
	}
	if !strings.Contains(err.Error(), "llmchat preflight for agent Otogi") {
		t.Fatalf("error = %q, want llmchat preflight context", err)
	}
	if !strings.Contains(err.Error(), "insufficient handler deadline budget") {
		t.Fatalf("error = %q, want insufficient handler deadline budget", err)
	}
	if !strings.Contains(err.Error(), "remaining=") {
		t.Fatalf("error = %q, want remaining deadline detail", err)
	}
	if !strings.Contains(err.Error(), "request_timeout=1m30s") {
		t.Fatalf("error = %q, want request timeout detail", err)
	}
	if !strings.Contains(err.Error(), "kernel.module_handler_timeout") {
		t.Fatalf("error = %q, want handler timeout guidance", err)
	}
	if providerCalled {
		t.Fatal("provider generate stream should not be called when deadline preflight fails")
	}
	if len(sink.sendRequests) != 1 {
		t.Fatalf("send request count = %d, want 1", len(sink.sendRequests))
	}
	if sink.sendRequests[0].Text != defaultThinkingPlaceholder {
		t.Fatalf("placeholder text = %q, want %q", sink.sendRequests[0].Text, defaultThinkingPlaceholder)
	}
	if len(sink.editRequests) != 1 {
		t.Fatalf("edit request count = %d, want 1", len(sink.editRequests))
	}
	if sink.editRequests[0].Text != placeholderFailureMessage {
		t.Fatalf("failure edit text = %q, want %q", sink.editRequests[0].Text, placeholderFailureMessage)
	}
}

func TestHandleArticleBuildFailureEditsPlaceholder(t *testing.T) {
	module, err := New(Config{
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
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	providerCalled := false
	sink := &sinkDispatcherStub{}
	module.dispatcher = sink
	module.memory = &memoryStub{
		replyErr: errors.New("reply chain failed"),
	}
	module.providers = map[string]otogi.LLMProvider{
		"openai": &providerStub{
			stream: &streamStub{chunks: []otogi.LLMGenerateChunk{{Delta: "unused"}}},
			onGenerateStream: func() {
				providerCalled = true
			},
		},
	}

	err = module.handleArticle(context.Background(), testLLMChatEvent("Otogi hello"))
	if err == nil {
		t.Fatal("handleArticle error = nil, want build failure")
	}
	if !strings.Contains(err.Error(), "llmchat build request for agent Otogi") {
		t.Fatalf("error = %q, want build request context", err)
	}
	if providerCalled {
		t.Fatal("provider generate stream should not be called on build failure")
	}
	if len(sink.sendRequests) != 1 {
		t.Fatalf("send request count = %d, want 1", len(sink.sendRequests))
	}
	if len(sink.editRequests) != 1 {
		t.Fatalf("edit request count = %d, want 1", len(sink.editRequests))
	}
	if sink.editRequests[0].MessageID != "msg-1" {
		t.Fatalf("failure edit message id = %q, want msg-1", sink.editRequests[0].MessageID)
	}
	if sink.editRequests[0].Text != placeholderFailureMessage {
		t.Fatalf("failure edit text = %q, want %q", sink.editRequests[0].Text, placeholderFailureMessage)
	}
}

func TestHandleArticleProviderStartFailureEditsPlaceholder(t *testing.T) {
	module, err := New(Config{
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
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	sink := &sinkDispatcherStub{}
	module.dispatcher = sink
	module.memory = &memoryStub{
		replyChain: []otogi.ReplyChainEntry{
			{
				Conversation: otogi.Conversation{ID: "chat-1", Type: otogi.ConversationTypeGroup},
				Actor:        otogi.Actor{ID: "user-1", DisplayName: "Alice"},
				Article:      otogi.Article{ID: "m-1", Text: "Otogi hello"},
				IsCurrent:    true,
			},
		},
	}
	module.providers = map[string]otogi.LLMProvider{
		"openai": &providerStub{
			streamErr: errors.New("provider start failed"),
		},
	}

	err = module.handleArticle(context.Background(), testLLMChatEvent("Otogi hello"))
	if err == nil {
		t.Fatal("handleArticle error = nil, want stream startup failure")
	}
	if !strings.Contains(err.Error(), "llmchat stream response for agent Otogi") {
		t.Fatalf("error = %q, want stream response context", err)
	}
	if len(sink.sendRequests) != 1 {
		t.Fatalf("send request count = %d, want 1", len(sink.sendRequests))
	}
	if len(sink.editRequests) != 1 {
		t.Fatalf("edit request count = %d, want 1", len(sink.editRequests))
	}
	if sink.editRequests[0].MessageID != "msg-1" {
		t.Fatalf("failure edit message id = %q, want msg-1", sink.editRequests[0].MessageID)
	}
	if sink.editRequests[0].Text != placeholderFailureMessage {
		t.Fatalf("failure edit text = %q, want %q", sink.editRequests[0].Text, placeholderFailureMessage)
	}
}

func TestHandleArticleThinkingOnlyStreamEditsPlaceholderFailure(t *testing.T) {
	module, err := New(Config{
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
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	sink := &sinkDispatcherStub{}
	module.dispatcher = sink
	module.memory = &memoryStub{
		replyChain: []otogi.ReplyChainEntry{
			{
				Conversation: otogi.Conversation{ID: "chat-1", Type: otogi.ConversationTypeGroup},
				Actor:        otogi.Actor{ID: "user-1", DisplayName: "Alice"},
				Article:      otogi.Article{ID: "m-1", Text: "Otogi hello"},
				IsCurrent:    true,
			},
		},
	}
	module.providers = map[string]otogi.LLMProvider{
		"openai": &providerStub{
			stream: &streamStub{chunks: []otogi.LLMGenerateChunk{
				{Kind: otogi.LLMGenerateChunkKindThinkingSummary, Delta: "draft steps"},
			}},
		},
	}
	module.clock = sequenceClock([]time.Time{
		time.Unix(0, 0).UTC(),
		time.Unix(0, 0).UTC(),
	})

	err = module.handleArticle(context.Background(), testLLMChatEvent("Otogi hello"))
	if err == nil {
		t.Fatal("handleArticle error = nil, want thinking-only stream failure")
	}
	if !strings.Contains(err.Error(), "llmchat stream response for agent Otogi") {
		t.Fatalf("error = %q, want stream response context", err)
	}
	if !strings.Contains(err.Error(), "no output text received") {
		t.Fatalf("error = %q, want no output text received", err)
	}
	if len(sink.sendRequests) != 1 {
		t.Fatalf("send request count = %d, want 1", len(sink.sendRequests))
	}
	if sink.sendRequests[0].Text != defaultThinkingPlaceholder {
		t.Fatalf("placeholder text = %q, want %q", sink.sendRequests[0].Text, defaultThinkingPlaceholder)
	}
	if len(sink.editRequests) != 2 {
		t.Fatalf("edit request count = %d, want 2", len(sink.editRequests))
	}
	if sink.editRequests[0].Text != "Thinking...\n\ndraft steps" {
		t.Fatalf("thinking edit text = %q, want %q", sink.editRequests[0].Text, "Thinking...\n\ndraft steps")
	}
	if sink.editRequests[1].MessageID != "msg-1" {
		t.Fatalf("failure edit message id = %q, want msg-1", sink.editRequests[1].MessageID)
	}
	if sink.editRequests[1].Text != placeholderFailureMessage {
		t.Fatalf("failure edit text = %q, want %q", sink.editRequests[1].Text, placeholderFailureMessage)
	}
}

func testLLMChatEvent(text string) *otogi.Event {
	return &otogi.Event{
		Kind:       otogi.EventKindArticleCreated,
		OccurredAt: time.Unix(1, 0).UTC(),
		Source:     otogi.EventSource{Platform: otogi.PlatformTelegram, ID: "tg-main"},
		Conversation: otogi.Conversation{
			ID:   "chat-1",
			Type: otogi.ConversationTypeGroup,
		},
		Actor:   otogi.Actor{ID: "user-1", DisplayName: "Alice"},
		Article: &otogi.Article{ID: "m-1", Text: text},
	}
}

type sinkDispatcherStub struct {
	sendRequests []otogi.SendMessageRequest
	editRequests []otogi.EditMessageRequest
	sendErrors   []error
	editErrors   []error
	onSend       func(otogi.SendMessageRequest)
	onEdit       func(otogi.EditMessageRequest)
}

func (s *sinkDispatcherStub) SendMessage(
	_ context.Context,
	req otogi.SendMessageRequest,
) (*otogi.OutboundMessage, error) {
	index := len(s.sendRequests)
	s.sendRequests = append(s.sendRequests, req)
	if s.onSend != nil {
		s.onSend(req)
	}
	if index < len(s.sendErrors) && s.sendErrors[index] != nil {
		return nil, s.sendErrors[index]
	}

	return &otogi.OutboundMessage{ID: fmt.Sprintf("msg-%d", index+1), Target: req.Target}, nil
}

func (s *sinkDispatcherStub) EditMessage(_ context.Context, req otogi.EditMessageRequest) error {
	index := len(s.editRequests)
	s.editRequests = append(s.editRequests, req)
	if s.onEdit != nil {
		s.onEdit(req)
	}
	if index < len(s.editErrors) {
		return s.editErrors[index]
	}

	return nil
}

func (*sinkDispatcherStub) DeleteMessage(context.Context, otogi.DeleteMessageRequest) error {
	return nil
}
func (*sinkDispatcherStub) SetReaction(context.Context, otogi.SetReactionRequest) error { return nil }
func (*sinkDispatcherStub) ListSinks(context.Context) ([]otogi.EventSink, error)        { return nil, nil }
func (*sinkDispatcherStub) ListSinksByPlatform(context.Context, otogi.Platform) ([]otogi.EventSink, error) {
	return nil, nil
}

type memoryStub struct {
	replyChain      []otogi.ReplyChainEntry
	replyErr        error
	onGetReplyChain func()
}

func (*memoryStub) Get(context.Context, otogi.MemoryLookup) (otogi.Memory, bool, error) {
	return otogi.Memory{}, false, nil
}

func (*memoryStub) GetReplied(context.Context, *otogi.Event) (otogi.Memory, bool, error) {
	return otogi.Memory{}, false, nil
}

func (m *memoryStub) GetReplyChain(context.Context, *otogi.Event) ([]otogi.ReplyChainEntry, error) {
	if m.onGetReplyChain != nil {
		m.onGetReplyChain()
	}
	if m.replyErr != nil {
		return nil, m.replyErr
	}

	cloned := make([]otogi.ReplyChainEntry, 0, len(m.replyChain))
	for _, entry := range m.replyChain {
		cloned = append(cloned, otogi.ReplyChainEntry{
			Conversation: entry.Conversation,
			Actor:        entry.Actor,
			Article:      entry.Article,
			IsCurrent:    entry.IsCurrent,
		})
	}

	return cloned, nil
}

type providerRegistryStub struct {
	providers map[string]otogi.LLMProvider
	err       error
}

func (r *providerRegistryStub) Resolve(provider string) (otogi.LLMProvider, error) {
	if r.err != nil {
		return nil, r.err
	}
	resolved, exists := r.providers[provider]
	if !exists {
		return nil, fmt.Errorf("provider %s not found", provider)
	}

	return resolved, nil
}

type providerStub struct {
	stream           otogi.LLMStream
	streamErr        error
	onGenerateStream func()
}

func (p *providerStub) GenerateStream(context.Context, otogi.LLMGenerateRequest) (otogi.LLMStream, error) {
	if p.onGenerateStream != nil {
		p.onGenerateStream()
	}
	if p.streamErr != nil {
		return nil, p.streamErr
	}
	if p.stream == nil {
		return nil, fmt.Errorf("nil stream")
	}

	return p.stream, nil
}

type streamStub struct {
	chunks   []otogi.LLMGenerateChunk
	index    int
	recvErr  error
	closeErr error
}

func (s *streamStub) Recv(context.Context) (otogi.LLMGenerateChunk, error) {
	if s.recvErr != nil {
		return otogi.LLMGenerateChunk{}, s.recvErr
	}
	if s.index >= len(s.chunks) {
		return otogi.LLMGenerateChunk{}, io.EOF
	}
	chunk := s.chunks[s.index]
	s.index++

	return chunk, nil
}

func (s *streamStub) Close() error {
	return s.closeErr
}

type moduleRuntimeStub struct {
	registry otogi.ServiceRegistry
}

func (s moduleRuntimeStub) Services() otogi.ServiceRegistry { return s.registry }

func (moduleRuntimeStub) Subscribe(
	context.Context,
	otogi.InterestSet,
	otogi.SubscriptionSpec,
	otogi.EventHandler,
) (otogi.Subscription, error) {
	return nil, nil
}

type serviceRegistryStub struct {
	values map[string]any
}

func (s serviceRegistryStub) Register(string, any) error { return nil }

func (s serviceRegistryStub) Resolve(name string) (any, error) {
	value, ok := s.values[name]
	if !ok {
		return nil, otogi.ErrServiceNotFound
	}

	return value, nil
}

func sequenceClock(times []time.Time) func() time.Time {
	if len(times) == 0 {
		return time.Now
	}

	index := 0
	return func() time.Time {
		if index >= len(times) {
			return times[len(times)-1]
		}
		current := times[index]
		index++
		return current
	}
}

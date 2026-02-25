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

func TestMatchTriggeredAgent(t *testing.T) {
	module, err := New(Config{
		RequestTimeout: time.Second,
		Providers: map[string]ProviderProfile{
			"p": {
				Type:   providerTypeOpenAI,
				APIKey: "sk-test",
			},
		},
		Agents: []Agent{
			{
				Name:                 "Otogi",
				Description:          "primary",
				Provider:             "p",
				Model:                "m",
				SystemPromptTemplate: "You are {{.AgentName}}",
			},
			{
				Name:                 "Ai",
				Description:          "short",
				Provider:             "p",
				Model:                "m",
				SystemPromptTemplate: "You are {{.AgentName}}",
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
		Providers: map[string]ProviderProfile{
			"p": {
				Type:   providerTypeOpenAI,
				APIKey: "sk-test",
			},
		},
		Agents: []Agent{
			{
				Name:                 "Otogi",
				Description:          "assistant",
				Provider:             "p",
				Model:                "gpt-test",
				SystemPromptTemplate: "You are {{.AgentName}} at {{.DateTimeUTC}}",
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
}

func TestOnRegisterResolvesProviders(t *testing.T) {
	module, err := New(Config{
		RequestTimeout: time.Second,
		Providers: map[string]ProviderProfile{
			"openai": {
				Type:   providerTypeOpenAI,
				APIKey: "sk-test",
			},
		},
		Agents: []Agent{
			{
				Name:                 "Otogi",
				Description:          "assistant",
				Provider:             "openai",
				Model:                "gpt-test",
				SystemPromptTemplate: "You are {{.AgentName}}",
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
		Providers: map[string]ProviderProfile{
			"openai": {
				Type:   providerTypeOpenAI,
				APIKey: "sk-test",
			},
		},
		Agents: []Agent{
			{
				Name:                 "Otogi",
				Description:          "assistant",
				Provider:             "openai",
				Model:                "gpt-test",
				SystemPromptTemplate: "You are {{.AgentName}}",
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
		Providers: map[string]ProviderProfile{
			"openai": {
				Type:   providerTypeOpenAI,
				APIKey: "sk-test",
			},
		},
		Agents: []Agent{
			{
				Name:                 "Otogi",
				Description:          "assistant",
				Provider:             "openai",
				Model:                "gpt-test",
				SystemPromptTemplate: "You are {{.AgentName}}",
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
		provider,
		req,
	); err != nil {
		t.Fatalf("streamProviderReply failed: %v", err)
	}

	if len(sink.sendRequests) != 2 {
		t.Fatalf("send request count = %d, want 2 (placeholder + fallback)", len(sink.sendRequests))
	}
	if sink.sendRequests[1].Text != "hello" {
		t.Fatalf("fallback text = %q, want hello", sink.sendRequests[1].Text)
	}
}

func TestHandleArticleIgnoresBotMessages(t *testing.T) {
	module, err := New(Config{
		RequestTimeout: time.Second,
		Providers: map[string]ProviderProfile{
			"openai": {
				Type:   providerTypeOpenAI,
				APIKey: "sk-test",
			},
		},
		Agents: []Agent{
			{
				Name:                 "Otogi",
				Description:          "assistant",
				Provider:             "openai",
				Model:                "gpt-test",
				SystemPromptTemplate: "You are {{.AgentName}}",
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

type sinkDispatcherStub struct {
	sendRequests []otogi.SendMessageRequest
	editRequests []otogi.EditMessageRequest
	sendErrors   []error
	editErrors   []error
}

func (s *sinkDispatcherStub) SendMessage(
	_ context.Context,
	req otogi.SendMessageRequest,
) (*otogi.OutboundMessage, error) {
	index := len(s.sendRequests)
	s.sendRequests = append(s.sendRequests, req)
	if index < len(s.sendErrors) && s.sendErrors[index] != nil {
		return nil, s.sendErrors[index]
	}

	return &otogi.OutboundMessage{ID: fmt.Sprintf("msg-%d", index+1), Target: req.Target}, nil
}

func (s *sinkDispatcherStub) EditMessage(_ context.Context, req otogi.EditMessageRequest) error {
	index := len(s.editRequests)
	s.editRequests = append(s.editRequests, req)
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
	replyChain []otogi.ReplyChainEntry
	replyErr   error
}

func (*memoryStub) Get(context.Context, otogi.MemoryLookup) (otogi.Memory, bool, error) {
	return otogi.Memory{}, false, nil
}

func (*memoryStub) GetReplied(context.Context, *otogi.Event) (otogi.Memory, bool, error) {
	return otogi.Memory{}, false, nil
}

func (m *memoryStub) GetReplyChain(context.Context, *otogi.Event) ([]otogi.ReplyChainEntry, error) {
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
	stream    otogi.LLMStream
	streamErr error
}

func (p *providerStub) GenerateStream(context.Context, otogi.LLMGenerateRequest) (otogi.LLMStream, error) {
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

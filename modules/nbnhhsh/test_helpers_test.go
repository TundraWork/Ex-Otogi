package nbnhhsh

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"ex-otogi/pkg/otogi/core"
	"ex-otogi/pkg/otogi/platform"
)

func newCommandEvent(text string, replyToID string) *platform.Event {
	candidate, matched, err := platform.ParseCommandCandidate(text)
	if err != nil {
		panic(err)
	}
	if !matched {
		panic("newCommandEvent expects command text")
	}

	return &platform.Event{
		ID:         "event-1",
		Kind:       platform.EventKindCommandReceived,
		OccurredAt: time.Unix(1, 0).UTC(),
		Source: platform.EventSource{
			Platform: platform.PlatformTelegram,
			ID:       "tg-main",
		},
		Conversation: platform.Conversation{
			ID:   "chat-42",
			Type: platform.ConversationTypePrivate,
		},
		Actor: platform.Actor{
			ID:       "user-1",
			Username: "tester",
		},
		Article: &platform.Article{
			ID:               "msg-1",
			Text:             text,
			ReplyToArticleID: replyToID,
		},
		Command: &platform.CommandInvocation{
			Name:            candidate.Name,
			Mention:         candidate.Mention,
			Value:           strings.Join(candidate.Tokens, " "),
			SourceEventID:   "source-1",
			SourceEventKind: platform.EventKindArticleCreated,
			RawInput:        text,
		},
	}
}

type captureDispatcher struct {
	sendErr     error
	messageID   string
	sendCalls   int
	lastRequest platform.SendMessageRequest
}

func (d *captureDispatcher) SendMessage(
	_ context.Context,
	request platform.SendMessageRequest,
) (*platform.OutboundMessage, error) {
	d.sendCalls++
	d.lastRequest = request
	if d.sendErr != nil {
		return nil, d.sendErr
	}

	return &platform.OutboundMessage{
		ID: d.messageID,
	}, nil
}

func (*captureDispatcher) EditMessage(context.Context, platform.EditMessageRequest) error {
	return nil
}

func (*captureDispatcher) DeleteMessage(context.Context, platform.DeleteMessageRequest) error {
	return nil
}

func (*captureDispatcher) SetReaction(context.Context, platform.SetReactionRequest) error {
	return nil
}

func (*captureDispatcher) ListSinks(context.Context) ([]platform.EventSink, error) {
	return nil, nil
}

func (*captureDispatcher) ListSinksByPlatform(
	context.Context,
	platform.Platform,
) ([]platform.EventSink, error) {
	return nil, nil
}

type memoryStub struct {
	replied      core.Memory
	repliedFound bool
	repliedErr   error
}

func (*memoryStub) Get(context.Context, core.MemoryLookup) (core.Memory, bool, error) {
	return core.Memory{}, false, nil
}

func (*memoryStub) GetBatch(context.Context, []core.MemoryLookup) (map[core.MemoryLookup]core.Memory, error) {
	return nil, nil
}

func (m *memoryStub) GetReplied(context.Context, *platform.Event) (core.Memory, bool, error) {
	return m.replied, m.repliedFound, m.repliedErr
}

func (*memoryStub) GetReplyChain(context.Context, *platform.Event) ([]core.ReplyChainEntry, error) {
	return nil, nil
}

func (*memoryStub) ListConversationContextBefore(
	context.Context,
	core.ConversationContextBeforeQuery,
) ([]core.ConversationContextEntry, error) {
	return nil, nil
}

type fakeGuessClient struct {
	lastText string
	calls    int
	results  []guessResult
	err      error
}

func (c *fakeGuessClient) Guess(_ context.Context, text string) ([]guessResult, error) {
	c.calls++
	c.lastText = text
	if c.err != nil {
		return nil, c.err
	}

	return append([]guessResult(nil), c.results...), nil
}

type moduleRuntimeStub struct {
	registry      core.ServiceRegistry
	configs       core.ConfigRegistry
	subscribeErr  error
	lastInterest  core.InterestSet
	lastSpec      core.SubscriptionSpec
	subscribeCall int
}

func (s *moduleRuntimeStub) Services() core.ServiceRegistry {
	return s.registry
}

func (s *moduleRuntimeStub) Config() core.ConfigRegistry {
	return s.configs
}

func (s *moduleRuntimeStub) Subscribe(
	_ context.Context,
	interest core.InterestSet,
	spec core.SubscriptionSpec,
	_ core.EventHandler,
) (core.Subscription, error) {
	s.subscribeCall++
	s.lastInterest = interest
	s.lastSpec = spec
	if s.subscribeErr != nil {
		return nil, s.subscribeErr
	}

	return nil, nil
}

type serviceRegistryStub struct {
	values map[string]any
}

func (s serviceRegistryStub) Register(string, any) error {
	return nil
}

func (s serviceRegistryStub) Resolve(name string) (any, error) {
	value, ok := s.values[name]
	if !ok {
		return nil, core.ErrServiceNotFound
	}

	return value, nil
}

type configRegistryStub struct {
	values map[string]json.RawMessage
}

func newConfigRegistryStub() *configRegistryStub {
	return &configRegistryStub{
		values: make(map[string]json.RawMessage),
	}
}

func (r *configRegistryStub) Register(moduleName string, raw json.RawMessage) error {
	if moduleName == "" {
		return fmt.Errorf("register module config: empty module name")
	}
	if len(raw) == 0 {
		return fmt.Errorf("register module config %s: empty config", moduleName)
	}
	if _, exists := r.values[moduleName]; exists {
		return fmt.Errorf("register module config %s: %w", moduleName, core.ErrConfigAlreadyRegistered)
	}

	r.values[moduleName] = append(json.RawMessage(nil), raw...)

	return nil
}

func (r *configRegistryStub) Resolve(moduleName string) (json.RawMessage, error) {
	raw, ok := r.values[moduleName]
	if !ok {
		return nil, fmt.Errorf("resolve module config %s: %w", moduleName, core.ErrConfigNotFound)
	}

	return append(json.RawMessage(nil), raw...), nil
}

func mustRegisterConfig(t testingT, registry *configRegistryStub, value any) {
	t.Helper()

	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := registry.Register(moduleName, raw); err != nil {
		t.Fatalf("register config: %v", err)
	}
}

type testingT interface {
	Helper()
	Fatalf(format string, args ...any)
}

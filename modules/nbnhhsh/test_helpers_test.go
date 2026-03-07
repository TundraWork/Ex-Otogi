package nbnhhsh

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"ex-otogi/pkg/otogi"
)

func newCommandEvent(text string, replyToID string) *otogi.Event {
	candidate, matched, err := otogi.ParseCommandCandidate(text)
	if err != nil {
		panic(err)
	}
	if !matched {
		panic("newCommandEvent expects command text")
	}

	return &otogi.Event{
		ID:         "event-1",
		Kind:       otogi.EventKindCommandReceived,
		OccurredAt: time.Unix(1, 0).UTC(),
		Source: otogi.EventSource{
			Platform: otogi.PlatformTelegram,
			ID:       "tg-main",
		},
		Conversation: otogi.Conversation{
			ID:   "chat-42",
			Type: otogi.ConversationTypePrivate,
		},
		Actor: otogi.Actor{
			ID:       "user-1",
			Username: "tester",
		},
		Article: &otogi.Article{
			ID:               "msg-1",
			Text:             text,
			ReplyToArticleID: replyToID,
		},
		Command: &otogi.CommandInvocation{
			Name:            candidate.Name,
			Mention:         candidate.Mention,
			Value:           strings.Join(candidate.Tokens, " "),
			SourceEventID:   "source-1",
			SourceEventKind: otogi.EventKindArticleCreated,
			RawInput:        text,
		},
	}
}

type captureDispatcher struct {
	sendErr     error
	messageID   string
	sendCalls   int
	lastRequest otogi.SendMessageRequest
}

func (d *captureDispatcher) SendMessage(
	_ context.Context,
	request otogi.SendMessageRequest,
) (*otogi.OutboundMessage, error) {
	d.sendCalls++
	d.lastRequest = request
	if d.sendErr != nil {
		return nil, d.sendErr
	}

	return &otogi.OutboundMessage{
		ID: d.messageID,
	}, nil
}

func (*captureDispatcher) EditMessage(context.Context, otogi.EditMessageRequest) error {
	return nil
}

func (*captureDispatcher) DeleteMessage(context.Context, otogi.DeleteMessageRequest) error {
	return nil
}

func (*captureDispatcher) SetReaction(context.Context, otogi.SetReactionRequest) error {
	return nil
}

func (*captureDispatcher) ListSinks(context.Context) ([]otogi.EventSink, error) {
	return nil, nil
}

func (*captureDispatcher) ListSinksByPlatform(
	context.Context,
	otogi.Platform,
) ([]otogi.EventSink, error) {
	return nil, nil
}

type memoryStub struct {
	replied      otogi.Memory
	repliedFound bool
	repliedErr   error
}

func (*memoryStub) Get(context.Context, otogi.MemoryLookup) (otogi.Memory, bool, error) {
	return otogi.Memory{}, false, nil
}

func (m *memoryStub) GetReplied(context.Context, *otogi.Event) (otogi.Memory, bool, error) {
	return m.replied, m.repliedFound, m.repliedErr
}

func (*memoryStub) GetReplyChain(context.Context, *otogi.Event) ([]otogi.ReplyChainEntry, error) {
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
	registry      otogi.ServiceRegistry
	configs       otogi.ConfigRegistry
	subscribeErr  error
	lastInterest  otogi.InterestSet
	lastSpec      otogi.SubscriptionSpec
	subscribeCall int
}

func (s *moduleRuntimeStub) Services() otogi.ServiceRegistry {
	return s.registry
}

func (s *moduleRuntimeStub) Config() otogi.ConfigRegistry {
	return s.configs
}

func (s *moduleRuntimeStub) Subscribe(
	_ context.Context,
	interest otogi.InterestSet,
	spec otogi.SubscriptionSpec,
	_ otogi.EventHandler,
) (otogi.Subscription, error) {
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
		return nil, otogi.ErrServiceNotFound
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
		return fmt.Errorf("register module config %s: %w", moduleName, otogi.ErrConfigAlreadyRegistered)
	}

	r.values[moduleName] = append(json.RawMessage(nil), raw...)

	return nil
}

func (r *configRegistryStub) Resolve(moduleName string) (json.RawMessage, error) {
	raw, ok := r.values[moduleName]
	if !ok {
		return nil, fmt.Errorf("resolve module config %s: %w", moduleName, otogi.ErrConfigNotFound)
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

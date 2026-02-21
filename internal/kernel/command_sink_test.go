package kernel

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ex-otogi/pkg/otogi"
)

func TestCommandDerivingSinkPublishesSourceAndDerivedCreatedEvent(t *testing.T) {
	t.Parallel()

	bus := NewEventBus(8, 1, time.Second, nil)
	t.Cleanup(func() {
		_ = bus.Close(context.Background())
	})

	received := make(chan *otogi.Event, 2)
	_, err := bus.Subscribe(
		context.Background(),
		otogi.InterestSet{},
		otogi.SubscriptionSpec{Name: "all-events", Buffer: 4, Workers: 1},
		func(_ context.Context, event *otogi.Event) error {
			received <- event
			return nil
		},
	)
	if err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}

	sink := &commandDerivingSink{
		base: bus,
		lookupCommand: func(prefix otogi.CommandPrefix, name string) (otogi.CommandSpec, bool) {
			if prefix == otogi.CommandPrefixOrdinary && name == "raw" {
				return otogi.CommandSpec{Prefix: otogi.CommandPrefixOrdinary, Name: "raw"}, true
			}
			return otogi.CommandSpec{}, false
		},
		serviceLookup: NewServiceRegistry(),
	}

	source := newSourceCreatedEvent("evt-1", "msg-1", "/raw 114514", "")
	if err := sink.Publish(context.Background(), source); err != nil {
		t.Fatalf("publish failed: %v", err)
	}

	first := waitEvent(t, received)
	second := waitEvent(t, received)

	if first.Kind != otogi.EventKindArticleCreated {
		t.Fatalf("first kind = %s, want %s", first.Kind, otogi.EventKindArticleCreated)
	}
	if second.Kind != otogi.EventKindCommandReceived {
		t.Fatalf("second kind = %s, want %s", second.Kind, otogi.EventKindCommandReceived)
	}
	if second.Command == nil {
		t.Fatal("expected command payload")
	}
	if second.Command.Name != "raw" {
		t.Fatalf("command name = %q, want raw", second.Command.Name)
	}
	if second.Command.Value != "114514" {
		t.Fatalf("command value = %q, want 114514", second.Command.Value)
	}
	if second.Command.SourceEventID != source.ID {
		t.Fatalf("source event id = %q, want %q", second.Command.SourceEventID, source.ID)
	}
}

func TestCommandDerivingSinkPublishesDerivedEditedCommandEvent(t *testing.T) {
	t.Parallel()

	bus := NewEventBus(8, 1, time.Second, nil)
	t.Cleanup(func() {
		_ = bus.Close(context.Background())
	})

	received := make(chan *otogi.Event, 1)
	_, err := bus.Subscribe(
		context.Background(),
		otogi.InterestSet{Kinds: []otogi.EventKind{otogi.EventKindSystemCommandReceived}},
		otogi.SubscriptionSpec{Name: "command-events", Buffer: 2, Workers: 1},
		func(_ context.Context, event *otogi.Event) error {
			received <- event
			return nil
		},
	)
	if err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}

	sink := &commandDerivingSink{
		base: bus,
		lookupCommand: func(prefix otogi.CommandPrefix, name string) (otogi.CommandSpec, bool) {
			if prefix == otogi.CommandPrefixSystem && name == "history" {
				return otogi.CommandSpec{Prefix: otogi.CommandPrefixSystem, Name: "history"}, true
			}
			return otogi.CommandSpec{}, false
		},
		serviceLookup: NewServiceRegistry(),
	}

	source := newSourceEditedEvent("evt-2", "msg-9", "~history")
	if err := sink.Publish(context.Background(), source); err != nil {
		t.Fatalf("publish failed: %v", err)
	}

	commandEvent := waitEvent(t, received)
	if commandEvent.Kind != otogi.EventKindSystemCommandReceived {
		t.Fatalf("kind = %s, want %s", commandEvent.Kind, otogi.EventKindSystemCommandReceived)
	}
	if commandEvent.Command == nil {
		t.Fatal("expected command payload")
	}
	if commandEvent.Command.Name != "history" {
		t.Fatalf("command name = %q, want history", commandEvent.Command.Name)
	}
	if commandEvent.Command.SourceEventKind != otogi.EventKindArticleEdited {
		t.Fatalf("source event kind = %q, want %q", commandEvent.Command.SourceEventKind, otogi.EventKindArticleEdited)
	}
	if commandEvent.Article == nil || commandEvent.Article.ID != "msg-9" {
		t.Fatalf("article = %+v, want id msg-9", commandEvent.Article)
	}
}

func TestCommandDerivingSinkUnregisteredCommandPublishesOnlySourceEvent(t *testing.T) {
	t.Parallel()

	bus := NewEventBus(8, 1, time.Second, nil)
	t.Cleanup(func() {
		_ = bus.Close(context.Background())
	})

	commandEvents := make(chan *otogi.Event, 1)
	_, err := bus.Subscribe(
		context.Background(),
		otogi.InterestSet{Kinds: []otogi.EventKind{otogi.EventKindCommandReceived}},
		otogi.SubscriptionSpec{Name: "command-events", Buffer: 1, Workers: 1},
		func(_ context.Context, event *otogi.Event) error {
			commandEvents <- event
			return nil
		},
	)
	if err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}

	sink := &commandDerivingSink{
		base: bus,
		lookupCommand: func(otogi.CommandPrefix, string) (otogi.CommandSpec, bool) {
			return otogi.CommandSpec{}, false
		},
		serviceLookup: NewServiceRegistry(),
	}

	if err := sink.Publish(context.Background(), newSourceCreatedEvent("evt-3", "msg-3", "/raw", "")); err != nil {
		t.Fatalf("publish failed: %v", err)
	}

	select {
	case event := <-commandEvents:
		t.Fatalf("unexpected command event: %+v", event)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestCommandDerivingSinkCommandBindingErrorRepliesAndSkipsDerivedEvent(t *testing.T) {
	t.Parallel()

	bus := NewEventBus(8, 1, time.Second, nil)
	t.Cleanup(func() {
		_ = bus.Close(context.Background())
	})

	commandEvents := make(chan *otogi.Event, 1)
	_, err := bus.Subscribe(
		context.Background(),
		otogi.InterestSet{Kinds: []otogi.EventKind{otogi.EventKindCommandReceived}},
		otogi.SubscriptionSpec{Name: "command-events", Buffer: 1, Workers: 1},
		func(_ context.Context, event *otogi.Event) error {
			commandEvents <- event
			return nil
		},
	)
	if err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}

	dispatcher := &commandReplyCaptureDispatcher{}
	services := NewServiceRegistry()
	if err := services.Register(otogi.ServiceSinkDispatcher, dispatcher); err != nil {
		t.Fatalf("register dispatcher failed: %v", err)
	}

	sink := &commandDerivingSink{
		base: bus,
		lookupCommand: func(prefix otogi.CommandPrefix, name string) (otogi.CommandSpec, bool) {
			if prefix == otogi.CommandPrefixOrdinary && name == "raw" {
				return otogi.CommandSpec{
					Prefix: otogi.CommandPrefixOrdinary,
					Name:   "raw",
					Options: []otogi.CommandOptionSpec{
						{Name: "article", Alias: "a", HasValue: true, Required: true},
					},
				}, true
			}

			return otogi.CommandSpec{}, false
		},
		serviceLookup: services,
	}

	if err := sink.Publish(context.Background(), newSourceCreatedEvent("evt-4", "msg-4", "/raw", "")); err != nil {
		t.Fatalf("publish failed: %v", err)
	}

	if dispatcher.calls.Load() != 1 {
		t.Fatalf("reply calls = %d, want 1", dispatcher.calls.Load())
	}
	if !strings.Contains(dispatcher.lastRequest.Text, "missing required option") {
		t.Fatalf("reply text = %q, want missing required option hint", dispatcher.lastRequest.Text)
	}
	if dispatcher.lastRequest.ReplyToMessageID != "msg-4" {
		t.Fatalf("reply_to = %q, want msg-4", dispatcher.lastRequest.ReplyToMessageID)
	}

	select {
	case event := <-commandEvents:
		t.Fatalf("unexpected derived command event: %+v", event)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestKernelRegisterModuleRejectsDuplicateCommandAcrossModules(t *testing.T) {
	t.Parallel()

	kernelRuntime := New()
	moduleA := &stubModule{
		name: "command-a",
		spec: otogi.ModuleSpec{
			Commands: []otogi.CommandSpec{
				{Prefix: otogi.CommandPrefixOrdinary, Name: "raw"},
			},
		},
	}
	moduleB := &stubModule{
		name: "command-b",
		spec: otogi.ModuleSpec{
			Commands: []otogi.CommandSpec{
				{Prefix: otogi.CommandPrefixOrdinary, Name: "raw"},
			},
		},
	}

	if err := kernelRuntime.RegisterModule(context.Background(), moduleA); err != nil {
		t.Fatalf("register module A failed: %v", err)
	}
	err := kernelRuntime.RegisterModule(context.Background(), moduleB)
	if err == nil {
		t.Fatal("expected duplicate command registration to fail")
	}
	if !strings.Contains(err.Error(), "already registered by module") {
		t.Fatalf("error = %v, want duplicate registration error", err)
	}
}

func waitEvent(t *testing.T, events <-chan *otogi.Event) *otogi.Event {
	t.Helper()

	select {
	case event := <-events:
		return event
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for event")
		return nil
	}
}

func newSourceCreatedEvent(id string, messageID string, text string, replyToID string) *otogi.Event {
	return &otogi.Event{
		ID:         id,
		Kind:       otogi.EventKindArticleCreated,
		OccurredAt: time.Unix(10, 0).UTC(),
		Platform:   otogi.PlatformTelegram,
		Conversation: otogi.Conversation{
			ID:   "chat-1",
			Type: otogi.ConversationTypeGroup,
		},
		Actor: otogi.Actor{ID: "actor-1"},
		Article: &otogi.Article{
			ID:               messageID,
			ReplyToArticleID: replyToID,
			Text:             text,
		},
	}
}

func newSourceEditedEvent(id string, targetArticleID string, text string) *otogi.Event {
	return &otogi.Event{
		ID:         id,
		Kind:       otogi.EventKindArticleEdited,
		OccurredAt: time.Unix(10, 0).UTC(),
		Platform:   otogi.PlatformTelegram,
		Conversation: otogi.Conversation{
			ID:   "chat-1",
			Type: otogi.ConversationTypeGroup,
		},
		Actor: otogi.Actor{ID: "actor-1"},
		Mutation: &otogi.ArticleMutation{
			Type:            otogi.MutationTypeEdit,
			TargetArticleID: targetArticleID,
			After: &otogi.ArticleSnapshot{
				Text: text,
			},
		},
	}
}

type commandReplyCaptureDispatcher struct {
	calls       atomic.Int64
	mu          sync.Mutex
	lastRequest otogi.SendMessageRequest
}

func (d *commandReplyCaptureDispatcher) SendMessage(
	_ context.Context,
	request otogi.SendMessageRequest,
) (*otogi.OutboundMessage, error) {
	d.calls.Add(1)
	d.mu.Lock()
	d.lastRequest = request
	d.mu.Unlock()

	return &otogi.OutboundMessage{ID: "out-1", Target: request.Target}, nil
}

func (*commandReplyCaptureDispatcher) EditMessage(context.Context, otogi.EditMessageRequest) error {
	return nil
}

func (*commandReplyCaptureDispatcher) DeleteMessage(context.Context, otogi.DeleteMessageRequest) error {
	return nil
}

func (*commandReplyCaptureDispatcher) SetReaction(context.Context, otogi.SetReactionRequest) error {
	return nil
}

func (*commandReplyCaptureDispatcher) ListSinks(context.Context) ([]otogi.EventSink, error) {
	return nil, nil
}

func (*commandReplyCaptureDispatcher) ListSinksByPlatform(
	context.Context,
	otogi.Platform,
) ([]otogi.EventSink, error) {
	return nil, nil
}

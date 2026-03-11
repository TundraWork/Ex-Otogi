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

	sink := &commandDerivingDispatcher{
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

func TestCommandDerivingSinkDoesNotReTriggerCommandsOnEditedSourceEvent(t *testing.T) {
	t.Parallel()

	bus := NewEventBus(8, 1, time.Second, nil)
	t.Cleanup(func() {
		_ = bus.Close(context.Background())
	})

	editedEvents := make(chan *otogi.Event, 1)
	commandEvents := make(chan *otogi.Event, 1)
	_, err := bus.Subscribe(
		context.Background(),
		otogi.InterestSet{Kinds: []otogi.EventKind{otogi.EventKindArticleEdited}},
		otogi.SubscriptionSpec{Name: "edited-events", Buffer: 2, Workers: 1},
		func(_ context.Context, event *otogi.Event) error {
			editedEvents <- event
			return nil
		},
	)
	if err != nil {
		t.Fatalf("subscribe edited events failed: %v", err)
	}
	_, err = bus.Subscribe(
		context.Background(),
		otogi.InterestSet{Kinds: []otogi.EventKind{otogi.EventKindSystemCommandReceived}},
		otogi.SubscriptionSpec{Name: "command-events", Buffer: 2, Workers: 1},
		func(_ context.Context, event *otogi.Event) error {
			commandEvents <- event
			return nil
		},
	)
	if err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}

	sink := &commandDerivingDispatcher{
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

	editedEvent := waitEvent(t, editedEvents)
	if editedEvent.Kind != otogi.EventKindArticleEdited {
		t.Fatalf("kind = %s, want %s", editedEvent.Kind, otogi.EventKindArticleEdited)
	}
	if editedEvent.Mutation == nil || editedEvent.Mutation.TargetArticleID != "msg-9" {
		t.Fatalf("mutation = %+v, want target article id msg-9", editedEvent.Mutation)
	}

	select {
	case event := <-commandEvents:
		t.Fatalf("unexpected derived command event from edit: %+v", event)
	case <-time.After(300 * time.Millisecond):
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

	sink := &commandDerivingDispatcher{
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

	sink := &commandDerivingDispatcher{
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

	kernelRuntime := newTestKernel(t)
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
		Source: otogi.EventSource{
			Platform: otogi.PlatformTelegram,
		},
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
		Source: otogi.EventSource{
			Platform: otogi.PlatformTelegram,
		},
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

func TestChatAllowlistConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		config      ChatAllowlistConfig
		wantEnabled bool
		sourceID    string
		chatID      string
		wantAllowed bool
		commandName string
		wantBypass  bool
	}{
		{
			name:        "empty config allows all",
			config:      ChatAllowlistConfig{},
			wantEnabled: false,
			sourceID:    "tg-src",
			chatID:      "any-chat",
			wantAllowed: true,
			commandName: "whoami",
			wantBypass:  false,
		},
		{
			name: "allowlisted qualified key is allowed",
			config: ChatAllowlistConfig{
				ConversationIDs: map[string]struct{}{"tg-src/chat-1": {}},
			},
			wantEnabled: true,
			sourceID:    "tg-src",
			chatID:      "chat-1",
			wantAllowed: true,
		},
		{
			name: "non-allowlisted qualified key is rejected",
			config: ChatAllowlistConfig{
				ConversationIDs: map[string]struct{}{"tg-src/chat-1": {}},
			},
			wantEnabled: true,
			sourceID:    "tg-src",
			chatID:      "chat-999",
			wantAllowed: false,
		},
		{
			name: "same chat different source is rejected",
			config: ChatAllowlistConfig{
				ConversationIDs: map[string]struct{}{"tg-src/chat-1": {}},
			},
			wantEnabled: true,
			sourceID:    "other-src",
			chatID:      "chat-1",
			wantAllowed: false,
		},
		{
			name: "bypass command matches",
			config: ChatAllowlistConfig{
				ConversationIDs: map[string]struct{}{"tg-src/chat-1": {}},
				BypassCommands:  map[string]struct{}{"whoami": {}},
			},
			wantEnabled: true,
			commandName: "whoami",
			wantBypass:  true,
		},
		{
			name: "bypass command case insensitive",
			config: ChatAllowlistConfig{
				BypassCommands: map[string]struct{}{"whoami": {}},
			},
			commandName: "WhoAmI",
			wantBypass:  true,
		},
		{
			name: "non-bypass command rejected",
			config: ChatAllowlistConfig{
				BypassCommands: map[string]struct{}{"whoami": {}},
			},
			commandName: "sleep",
			wantBypass:  false,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			if testCase.config.IsEnabled() != testCase.wantEnabled {
				t.Fatalf("IsEnabled() = %v, want %v", testCase.config.IsEnabled(), testCase.wantEnabled)
			}
			if testCase.chatID != "" {
				got := testCase.config.IsConversationAllowed(testCase.sourceID, testCase.chatID)
				if got != testCase.wantAllowed {
					t.Fatalf(
						"IsConversationAllowed(%q, %q) = %v, want %v",
						testCase.sourceID, testCase.chatID, got, testCase.wantAllowed,
					)
				}
			}
			if testCase.commandName != "" {
				got := testCase.config.IsBypassCommand(testCase.commandName)
				if got != testCase.wantBypass {
					t.Fatalf("IsBypassCommand(%q) = %v, want %v", testCase.commandName, got, testCase.wantBypass)
				}
			}
		})
	}
}

func TestCommandDerivingDispatcherAllowlistDropsNonAllowlistedEvents(t *testing.T) {
	t.Parallel()

	bus := NewEventBus(8, 1, time.Second, nil)
	t.Cleanup(func() {
		_ = bus.Close(context.Background())
	})

	allEvents := make(chan *otogi.Event, 4)
	_, err := bus.Subscribe(
		context.Background(),
		otogi.InterestSet{},
		otogi.SubscriptionSpec{Name: "all-events", Buffer: 4, Workers: 1},
		func(_ context.Context, event *otogi.Event) error {
			allEvents <- event
			return nil
		},
	)
	if err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}

	sink := &commandDerivingDispatcher{
		base: bus,
		lookupCommand: func(prefix otogi.CommandPrefix, name string) (otogi.CommandSpec, bool) {
			if prefix == otogi.CommandPrefixOrdinary && name == "ping" {
				return otogi.CommandSpec{Prefix: otogi.CommandPrefixOrdinary, Name: "ping"}, true
			}
			return otogi.CommandSpec{}, false
		},
		serviceLookup: NewServiceRegistry(),
		allowlist: ChatAllowlistConfig{
			ConversationIDs: map[string]struct{}{"tg-src/chat-allowed": {}},
		},
	}

	source := newSourceCreatedEventInChat("evt-1", "msg-1", "/ping", "tg-src", "chat-blocked")
	if err := sink.Publish(context.Background(), source); err != nil {
		t.Fatalf("publish failed: %v", err)
	}

	select {
	case event := <-allEvents:
		t.Fatalf("unexpected event from non-allowlisted chat: %+v", event)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestCommandDerivingDispatcherAllowlistPassesAllowlistedChat(t *testing.T) {
	t.Parallel()

	bus := NewEventBus(8, 1, time.Second, nil)
	t.Cleanup(func() {
		_ = bus.Close(context.Background())
	})

	allEvents := make(chan *otogi.Event, 4)
	_, err := bus.Subscribe(
		context.Background(),
		otogi.InterestSet{},
		otogi.SubscriptionSpec{Name: "all-events", Buffer: 4, Workers: 1},
		func(_ context.Context, event *otogi.Event) error {
			allEvents <- event
			return nil
		},
	)
	if err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}

	sink := &commandDerivingDispatcher{
		base: bus,
		lookupCommand: func(prefix otogi.CommandPrefix, name string) (otogi.CommandSpec, bool) {
			if prefix == otogi.CommandPrefixOrdinary && name == "ping" {
				return otogi.CommandSpec{Prefix: otogi.CommandPrefixOrdinary, Name: "ping"}, true
			}
			return otogi.CommandSpec{}, false
		},
		serviceLookup: NewServiceRegistry(),
		allowlist: ChatAllowlistConfig{
			ConversationIDs: map[string]struct{}{"tg-src/chat-allowed": {}},
		},
	}

	source := newSourceCreatedEventInChat("evt-1", "msg-1", "/ping", "tg-src", "chat-allowed")
	if err := sink.Publish(context.Background(), source); err != nil {
		t.Fatalf("publish failed: %v", err)
	}

	first := waitEvent(t, allEvents)
	second := waitEvent(t, allEvents)

	if first.Kind != otogi.EventKindArticleCreated {
		t.Fatalf("first kind = %s, want %s", first.Kind, otogi.EventKindArticleCreated)
	}
	if second.Kind != otogi.EventKindCommandReceived {
		t.Fatalf("second kind = %s, want %s", second.Kind, otogi.EventKindCommandReceived)
	}
}

func TestCommandDerivingDispatcherAllowlistBypassSystemCommand(t *testing.T) {
	t.Parallel()

	bus := NewEventBus(8, 1, time.Second, nil)
	t.Cleanup(func() {
		_ = bus.Close(context.Background())
	})

	allEvents := make(chan *otogi.Event, 4)
	_, err := bus.Subscribe(
		context.Background(),
		otogi.InterestSet{},
		otogi.SubscriptionSpec{Name: "all-events", Buffer: 4, Workers: 1},
		func(_ context.Context, event *otogi.Event) error {
			allEvents <- event
			return nil
		},
	)
	if err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}

	sink := &commandDerivingDispatcher{
		base: bus,
		lookupCommand: func(prefix otogi.CommandPrefix, name string) (otogi.CommandSpec, bool) {
			if prefix == otogi.CommandPrefixSystem && name == "whoami" {
				return otogi.CommandSpec{Prefix: otogi.CommandPrefixSystem, Name: "whoami"}, true
			}
			return otogi.CommandSpec{}, false
		},
		serviceLookup: NewServiceRegistry(),
		allowlist: ChatAllowlistConfig{
			ConversationIDs: map[string]struct{}{"tg-src/chat-allowed": {}},
			BypassCommands:  map[string]struct{}{"whoami": {}},
		},
	}

	source := newSourceCreatedEventInChat("evt-1", "msg-1", "~whoami", "tg-src", "chat-blocked")
	if err := sink.Publish(context.Background(), source); err != nil {
		t.Fatalf("publish failed: %v", err)
	}

	event := waitEvent(t, allEvents)
	if event.Kind != otogi.EventKindSystemCommandReceived {
		t.Fatalf("kind = %s, want %s", event.Kind, otogi.EventKindSystemCommandReceived)
	}
	if event.Command == nil || event.Command.Name != "whoami" {
		t.Fatalf("command = %+v, want whoami", event.Command)
	}

	// Source article.created event must NOT have been published.
	select {
	case extra := <-allEvents:
		t.Fatalf("unexpected extra event: kind=%s", extra.Kind)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestCommandDerivingDispatcherAllowlistBypassRejectsNonBypassSystemCommand(t *testing.T) {
	t.Parallel()

	bus := NewEventBus(8, 1, time.Second, nil)
	t.Cleanup(func() {
		_ = bus.Close(context.Background())
	})

	allEvents := make(chan *otogi.Event, 4)
	_, err := bus.Subscribe(
		context.Background(),
		otogi.InterestSet{},
		otogi.SubscriptionSpec{Name: "all-events", Buffer: 4, Workers: 1},
		func(_ context.Context, event *otogi.Event) error {
			allEvents <- event
			return nil
		},
	)
	if err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}

	sink := &commandDerivingDispatcher{
		base: bus,
		lookupCommand: func(prefix otogi.CommandPrefix, name string) (otogi.CommandSpec, bool) {
			if prefix == otogi.CommandPrefixSystem && name == "sleep" {
				return otogi.CommandSpec{Prefix: otogi.CommandPrefixSystem, Name: "sleep"}, true
			}
			return otogi.CommandSpec{}, false
		},
		serviceLookup: NewServiceRegistry(),
		allowlist: ChatAllowlistConfig{
			ConversationIDs: map[string]struct{}{"tg-src/chat-allowed": {}},
			BypassCommands:  map[string]struct{}{"whoami": {}},
		},
	}

	source := newSourceCreatedEventInChat("evt-1", "msg-1", "~sleep", "tg-src", "chat-blocked")
	if err := sink.Publish(context.Background(), source); err != nil {
		t.Fatalf("publish failed: %v", err)
	}

	select {
	case event := <-allEvents:
		t.Fatalf("unexpected event for non-bypass system command: %+v", event)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestCommandDerivingDispatcherAllowlistBypassRejectsOrdinaryCommand(t *testing.T) {
	t.Parallel()

	bus := NewEventBus(8, 1, time.Second, nil)
	t.Cleanup(func() {
		_ = bus.Close(context.Background())
	})

	allEvents := make(chan *otogi.Event, 4)
	_, err := bus.Subscribe(
		context.Background(),
		otogi.InterestSet{},
		otogi.SubscriptionSpec{Name: "all-events", Buffer: 4, Workers: 1},
		func(_ context.Context, event *otogi.Event) error {
			allEvents <- event
			return nil
		},
	)
	if err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}

	sink := &commandDerivingDispatcher{
		base: bus,
		lookupCommand: func(prefix otogi.CommandPrefix, name string) (otogi.CommandSpec, bool) {
			if prefix == otogi.CommandPrefixOrdinary && name == "ping" {
				return otogi.CommandSpec{Prefix: otogi.CommandPrefixOrdinary, Name: "ping"}, true
			}
			return otogi.CommandSpec{}, false
		},
		serviceLookup: NewServiceRegistry(),
		allowlist: ChatAllowlistConfig{
			ConversationIDs: map[string]struct{}{"tg-src/chat-allowed": {}},
			BypassCommands:  map[string]struct{}{"ping": {}},
		},
	}

	// Even though "ping" is in bypass_commands, ordinary commands (/) should not bypass.
	source := newSourceCreatedEventInChat("evt-1", "msg-1", "/ping", "tg-src", "chat-blocked")
	if err := sink.Publish(context.Background(), source); err != nil {
		t.Fatalf("publish failed: %v", err)
	}

	select {
	case event := <-allEvents:
		t.Fatalf("unexpected event for ordinary command in blocked chat: %+v", event)
	case <-time.After(300 * time.Millisecond):
	}
}

func newSourceCreatedEventInChat(
	id string,
	messageID string,
	text string,
	sourceID string,
	chatID string,
) *otogi.Event {
	event := newSourceCreatedEvent(id, messageID, text, "")
	event.Source.ID = sourceID
	event.Conversation.ID = chatID

	return event
}

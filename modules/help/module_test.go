package help

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"ex-otogi/pkg/otogi/core"
	"ex-otogi/pkg/otogi/platform"
)

func TestModuleHandleCommand(t *testing.T) {
	tests := []struct {
		name             string
		event            *platform.Event
		catalogCommands  []core.RegisteredCommand
		catalogErr       error
		sendErr          error
		wantErr          bool
		wantSentHelp     bool
		wantTextContains []string
	}{
		{
			name:  "help command renders registered commands",
			event: newCommandEvent("/help"),
			catalogCommands: []core.RegisteredCommand{
				{
					ModuleName: "memory",
					Command: platform.CommandSpec{
						Prefix:      platform.CommandPrefixSystem,
						Name:        "raw",
						Description: "raw projection",
						Options: []platform.CommandOptionSpec{
							{Name: "article", Alias: "a", HasValue: true},
						},
					},
				},
				{
					ModuleName: "pingpong",
					Command: platform.CommandSpec{
						Prefix:      platform.CommandPrefixOrdinary,
						Name:        "ping",
						Description: "reply with pong!",
					},
				},
				{
					ModuleName: "help",
					Command: platform.CommandSpec{
						Prefix:      platform.CommandPrefixOrdinary,
						Name:        "help",
						Description: "show all available commands",
					},
				},
			},
			wantSentHelp: true,
			wantTextContains: []string{
				"Available commands:",
				"/help",
				"show all available commands",
				"(help)",
				"/ping",
				"reply with pong!",
				"(pingpong)",
				"~raw",
				"usage: --article|-a <value>",
				"raw projection",
				"(memory)",
			},
		},
		{
			name:         "non-help command ignored",
			event:        newCommandEvent("/ping"),
			wantSentHelp: false,
		},
		{
			name:         "system help command ignored",
			event:        newCommandEvent("~help"),
			wantSentHelp: false,
		},
		{
			name:         "missing command payload ignored",
			event:        newMissingCommandPayloadEvent(),
			wantSentHelp: false,
		},
		{
			name:         "catalog error returns error",
			event:        newCommandEvent("/help"),
			catalogErr:   errors.New("catalog failure"),
			wantErr:      true,
			wantSentHelp: false,
		},
		{
			name:         "send error returns error",
			event:        newCommandEvent("/help"),
			catalogErr:   nil,
			sendErr:      errors.New("dispatcher failure"),
			wantErr:      true,
			wantSentHelp: true,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			module := New()
			dispatcher := &captureDispatcher{
				messageID: "sent-1",
				sendErr:   testCase.sendErr,
			}
			commandCatalog := &captureCommandCatalog{
				commands: testCase.catalogCommands,
				err:      testCase.catalogErr,
			}
			module.dispatcher = dispatcher
			module.commandCatalog = commandCatalog

			err := module.handleCommand(context.Background(), testCase.event)
			if testCase.wantErr && err == nil {
				t.Fatal("expected error")
			}
			if !testCase.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			sentHelp := dispatcher.calls.Load() > 0
			if sentHelp != testCase.wantSentHelp {
				t.Fatalf("sent help = %v, want %v", sentHelp, testCase.wantSentHelp)
			}
			if !sentHelp {
				return
			}

			if dispatcher.lastRequest.ReplyToMessageID != testCase.event.Article.ID {
				t.Fatalf(
					"reply_to = %q, want %q",
					dispatcher.lastRequest.ReplyToMessageID,
					testCase.event.Article.ID,
				)
			}
			if dispatcher.lastRequest.Target.Sink == nil {
				t.Fatal("target sink = nil, want source sink")
			}
			if dispatcher.lastRequest.Target.Sink.ID != "tg-main" {
				t.Fatalf("target sink id = %q, want tg-main", dispatcher.lastRequest.Target.Sink.ID)
			}
			if len(dispatcher.lastRequest.Entities) != 1 {
				t.Fatalf("entities len = %d, want 1", len(dispatcher.lastRequest.Entities))
			}
			entity := dispatcher.lastRequest.Entities[0]
			if entity.Type != platform.TextEntityTypeBlockquote {
				t.Fatalf("entity type = %q, want %q", entity.Type, platform.TextEntityTypeBlockquote)
			}
			if !entity.Collapsed {
				t.Fatal("entity collapsed = false, want true")
			}
			if entity.Offset != 0 {
				t.Fatalf("entity offset = %d, want 0", entity.Offset)
			}
			if entity.Length != utf8.RuneCountInString(dispatcher.lastRequest.Text) {
				t.Fatalf(
					"entity length = %d, want %d",
					entity.Length,
					utf8.RuneCountInString(dispatcher.lastRequest.Text),
				)
			}
			for _, wantSubstring := range testCase.wantTextContains {
				if !strings.Contains(dispatcher.lastRequest.Text, wantSubstring) {
					t.Fatalf("text = %q, missing substring %q", dispatcher.lastRequest.Text, wantSubstring)
				}
			}
		})
	}
}

func TestModuleOnRegister(t *testing.T) {
	tests := []struct {
		name             string
		services         map[string]any
		wantErrSubstring string
	}{
		{
			name: "resolve dependencies succeeds",
			services: map[string]any{
				platform.ServiceSinkDispatcher: &captureDispatcher{},
				core.ServiceCommandCatalog:     &captureCommandCatalog{},
			},
		},
		{
			name: "missing outbound dispatcher fails",
			services: map[string]any{
				core.ServiceCommandCatalog: &captureCommandCatalog{},
			},
			wantErrSubstring: "help resolve outbound dispatcher",
		},
		{
			name: "missing command catalog fails",
			services: map[string]any{
				platform.ServiceSinkDispatcher: &captureDispatcher{},
			},
			wantErrSubstring: "help resolve command catalog",
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			module := New()
			registry := serviceRegistryStub{values: testCase.services}
			err := module.OnRegister(context.Background(), moduleRuntimeStub{registry: registry})

			if testCase.wantErrSubstring == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if testCase.wantErrSubstring != "" {
				if err == nil {
					t.Fatalf("expected error containing %q", testCase.wantErrSubstring)
				}
				if !strings.Contains(err.Error(), testCase.wantErrSubstring) {
					t.Fatalf("error = %v, want substring %q", err, testCase.wantErrSubstring)
				}
			}
		})
	}
}

func TestModuleSpecUsesCommandCapability(t *testing.T) {
	t.Parallel()

	module := New()
	spec := module.Spec()
	if len(spec.Handlers) != 1 {
		t.Fatalf("handler count = %d, want 1", len(spec.Handlers))
	}
	if len(spec.Commands) != 1 {
		t.Fatalf("command count = %d, want 1", len(spec.Commands))
	}

	handler := spec.Handlers[0]
	if !handler.Capability.Interest.RequireCommand {
		t.Fatal("expected RequireCommand to be true")
	}
	if !handler.Capability.Interest.RequireArticle {
		t.Fatal("expected RequireArticle to be true")
	}
	if len(handler.Capability.Interest.Kinds) != 1 || handler.Capability.Interest.Kinds[0] != platform.EventKindCommandReceived {
		t.Fatalf("kinds = %v, want [%s]", handler.Capability.Interest.Kinds, platform.EventKindCommandReceived)
	}
	if len(handler.Capability.Interest.CommandNames) != 1 || handler.Capability.Interest.CommandNames[0] != helpCommandName {
		t.Fatalf("command names = %v, want [%s]", handler.Capability.Interest.CommandNames, helpCommandName)
	}
}

func newCommandEvent(text string) *platform.Event {
	candidate, matched, err := platform.ParseCommandCandidate(text)
	if err != nil {
		panic(err)
	}
	if !matched {
		panic("newCommandEvent expects command text")
	}
	commandKind := platform.EventKindCommandReceived
	if candidate.Prefix == platform.CommandPrefixSystem {
		commandKind = platform.EventKindSystemCommandReceived
	}

	return &platform.Event{
		ID:         "event-1",
		Kind:       commandKind,
		OccurredAt: time.Unix(1, 0).UTC(),
		Source: platform.EventSource{
			Platform: platform.PlatformTelegram,
			ID:       "tg-main",
		},
		Conversation: platform.Conversation{
			ID:   "42",
			Type: platform.ConversationTypePrivate,
		},
		Article: &platform.Article{
			ID:   "msg-1",
			Text: text,
		},
		Command: &platform.CommandInvocation{
			Name:            candidate.Name,
			Mention:         candidate.Mention,
			Value:           strings.Join(candidate.Tokens, " "),
			SourceEventID:   "source-event-1",
			SourceEventKind: platform.EventKindArticleCreated,
			RawInput:        text,
		},
	}
}

func newMissingCommandPayloadEvent() *platform.Event {
	return &platform.Event{
		ID:         "event-1",
		Kind:       platform.EventKindCommandReceived,
		OccurredAt: time.Unix(1, 0).UTC(),
		Source: platform.EventSource{
			Platform: platform.PlatformTelegram,
			ID:       "tg-main",
		},
		Conversation: platform.Conversation{
			ID:   "42",
			Type: platform.ConversationTypePrivate,
		},
		Article: &platform.Article{
			ID:   "msg-1",
			Text: "/help",
		},
	}
}

type captureDispatcher struct {
	calls       atomic.Int64
	messageID   string
	sendErr     error
	lastRequest platform.SendMessageRequest
}

func (d *captureDispatcher) SendMessage(
	_ context.Context,
	request platform.SendMessageRequest,
) (*platform.OutboundMessage, error) {
	d.calls.Add(1)
	d.lastRequest = request
	if d.sendErr != nil {
		return nil, d.sendErr
	}

	return &platform.OutboundMessage{ID: d.messageID, Target: request.Target}, nil
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

type captureCommandCatalog struct {
	commands []core.RegisteredCommand
	err      error
}

func (c *captureCommandCatalog) ListCommands(context.Context) ([]core.RegisteredCommand, error) {
	if c.err != nil {
		return nil, c.err
	}

	return append([]core.RegisteredCommand(nil), c.commands...), nil
}

type moduleRuntimeStub struct {
	registry core.ServiceRegistry
	configs  core.ConfigRegistry
}

func (s moduleRuntimeStub) Services() core.ServiceRegistry {
	return s.registry
}

func (s moduleRuntimeStub) Config() core.ConfigRegistry {
	return s.configs
}

func (moduleRuntimeStub) Subscribe(
	context.Context,
	core.InterestSet,
	core.SubscriptionSpec,
	core.EventHandler,
) (core.Subscription, error) {
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

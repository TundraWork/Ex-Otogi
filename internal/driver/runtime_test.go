package driver

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"testing"
	"time"

	"ex-otogi/pkg/otogi"
)

const (
	testDriverType     = "test-chat"
	testDriverTypeAlt  = "test-chat-alt"
	testDriverPlatform = otogi.Platform("test-platform")
)

func TestNewRegistryRejectsDuplicateDescriptorType(t *testing.T) {
	t.Parallel()

	_, err := NewRegistry([]Descriptor{
		{
			Type:     testDriverType,
			Platform: testDriverPlatform,
			Builder:  stubRuntimeBuilder,
		},
		{
			Type:     testDriverType,
			Platform: testDriverPlatform,
			Builder:  stubRuntimeBuilder,
		},
	})
	if err == nil {
		t.Fatal("expected duplicate descriptor type error")
	}
}

func TestNewRegistryRejectsEmptyPlatform(t *testing.T) {
	t.Parallel()

	_, err := NewRegistry([]Descriptor{
		{
			Type:     testDriverType,
			Platform: "",
			Builder:  stubRuntimeBuilder,
		},
	})
	if err == nil {
		t.Fatal("expected empty platform error")
	}
}

func TestRegistryPlatformForType(t *testing.T) {
	t.Parallel()

	registry, err := NewRegistry([]Descriptor{
		{
			Type:     testDriverType,
			Platform: testDriverPlatform,
			Builder:  stubRuntimeBuilder,
		},
	})
	if err != nil {
		t.Fatalf("new registry failed: %v", err)
	}

	platform, err := registry.PlatformForType(testDriverType)
	if err != nil {
		t.Fatalf("platform for type failed: %v", err)
	}
	if platform != testDriverPlatform {
		t.Fatalf("platform = %s, want %s", platform, testDriverPlatform)
	}

	if _, err := registry.PlatformForType("unknown"); err == nil {
		t.Fatal("expected unknown type error")
	}
}

func TestRegistryTypesSorted(t *testing.T) {
	t.Parallel()

	registry, err := NewRegistry([]Descriptor{
		{
			Type:     testDriverTypeAlt,
			Platform: otogi.Platform("test-platform-alt"),
			Builder:  stubRuntimeBuilder,
		},
		{
			Type:     testDriverType,
			Platform: testDriverPlatform,
			Builder:  stubRuntimeBuilder,
		},
	})
	if err != nil {
		t.Fatalf("new registry failed: %v", err)
	}

	types := registry.Types()
	want := []string{testDriverType, testDriverTypeAlt}
	if !slices.Equal(types, want) {
		t.Fatalf("types = %v, want %v", types, want)
	}
}

func TestRegistryBuildEnabled(t *testing.T) {
	t.Parallel()

	registry, err := NewRegistry([]Descriptor{
		{
			Type:     testDriverType,
			Platform: testDriverPlatform,
			Builder: func(
				_ context.Context,
				definition Definition,
				_ *slog.Logger,
			) (Runtime, error) {
				if definition.Name == "broken" {
					return Runtime{}, errors.New("broken build")
				}

				return Runtime{
					Source: otogi.EventSource{
						Platform: testDriverPlatform,
					},
					Driver: stubDriver{name: definition.Name},
				}, nil
			},
		},
	})
	if err != nil {
		t.Fatalf("new registry failed: %v", err)
	}

	_, err = registry.BuildEnabled(context.Background(), []Definition{
		{Name: "primary", Type: testDriverType, Enabled: true, Config: []byte("{}")},
		{Name: "broken", Type: testDriverType, Enabled: true, Config: []byte("{}")},
	}, slog.Default())
	if err == nil {
		t.Fatal("expected build error")
	}
}

func TestSinkDispatcherRoutesByID(t *testing.T) {
	t.Parallel()

	primary := &stubSinkDispatcher{}
	secondary := &stubSinkDispatcher{}
	dispatcher, err := NewSinkDispatcher([]Runtime{
		{
			Source: otogi.EventSource{
				Platform: testDriverPlatform,
				ID:       "main",
			},
			SinkDispatcher: primary,
		},
		{
			Source: otogi.EventSource{
				Platform: testDriverPlatform,
				ID:       "alt",
			},
			SinkDispatcher: secondary,
		},
	})
	if err != nil {
		t.Fatalf("new sink dispatcher failed: %v", err)
	}

	_, err = dispatcher.SendMessage(context.Background(), otogi.SendMessageRequest{
		Target: otogi.OutboundTarget{
			Conversation: otogi.Conversation{ID: "1", Type: otogi.ConversationTypeGroup},
			Sink: &otogi.EventSink{
				ID: "main",
			},
		},
		Text: "hello",
	})
	if err != nil {
		t.Fatalf("send message failed: %v", err)
	}
	if primary.sendCalls != 1 {
		t.Fatalf("primary calls = %d, want 1", primary.sendCalls)
	}
	if secondary.sendCalls != 0 {
		t.Fatalf("secondary calls = %d, want 0", secondary.sendCalls)
	}
}

func TestSinkDispatcherAmbiguousPlatform(t *testing.T) {
	t.Parallel()

	dispatcher, err := NewSinkDispatcher([]Runtime{
		{
			Source:         otogi.EventSource{Platform: testDriverPlatform, ID: "main"},
			SinkDispatcher: &stubSinkDispatcher{},
		},
		{
			Source:         otogi.EventSource{Platform: testDriverPlatform, ID: "alt"},
			SinkDispatcher: &stubSinkDispatcher{},
		},
	})
	if err != nil {
		t.Fatalf("new sink dispatcher failed: %v", err)
	}

	_, err = dispatcher.SendMessage(context.Background(), otogi.SendMessageRequest{
		Target: otogi.OutboundTarget{
			Conversation: otogi.Conversation{ID: "1", Type: otogi.ConversationTypeGroup},
			Sink: &otogi.EventSink{
				Platform: testDriverPlatform,
			},
		},
		Text: "hello",
	})
	if err == nil {
		t.Fatal("expected ambiguity error")
	}
}

func TestSinkDispatcherListSinksByPlatform(t *testing.T) {
	t.Parallel()

	dispatcher, err := NewSinkDispatcher([]Runtime{
		{
			Source:         otogi.EventSource{Platform: testDriverPlatform, ID: "main"},
			SinkDispatcher: &stubSinkDispatcher{},
		},
	})
	if err != nil {
		t.Fatalf("new sink dispatcher failed: %v", err)
	}

	sinks, err := dispatcher.ListSinksByPlatform(context.Background(), testDriverPlatform)
	if err != nil {
		t.Fatalf("list sinks by platform failed: %v", err)
	}
	if len(sinks) != 1 {
		t.Fatalf("sinks len = %d, want 1", len(sinks))
	}
	if sinks[0].ID != "main" {
		t.Fatalf("sink id = %s, want main", sinks[0].ID)
	}
}

type stubDriver struct {
	name string
}

func (d stubDriver) Name() string {
	return d.name
}

func (d stubDriver) Start(_ context.Context, _ otogi.EventDispatcher) error {
	return nil
}

func (d stubDriver) Shutdown(_ context.Context) error {
	return nil
}

type stubSinkDispatcher struct {
	sendCalls int
}

func (d *stubSinkDispatcher) SendMessage(
	_ context.Context,
	request otogi.SendMessageRequest,
) (*otogi.OutboundMessage, error) {
	d.sendCalls++

	return &otogi.OutboundMessage{
		ID: "1",
		Target: otogi.OutboundTarget{
			Conversation: request.Target.Conversation,
			Sink:         request.Target.Sink,
		},
	}, nil
}

func (*stubSinkDispatcher) EditMessage(context.Context, otogi.EditMessageRequest) error {
	return nil
}

func (*stubSinkDispatcher) DeleteMessage(context.Context, otogi.DeleteMessageRequest) error {
	return nil
}

func (*stubSinkDispatcher) SetReaction(context.Context, otogi.SetReactionRequest) error {
	return nil
}

func (*stubSinkDispatcher) ListSinks(context.Context) ([]otogi.EventSink, error) {
	return nil, nil
}

func (*stubSinkDispatcher) ListSinksByPlatform(
	context.Context,
	otogi.Platform,
) ([]otogi.EventSink, error) {
	return nil, nil
}

func stubRuntimeBuilder(_ context.Context, definition Definition, _ *slog.Logger) (Runtime, error) {
	return Runtime{
		Source: otogi.EventSource{
			Platform: testDriverPlatform,
		},
		Driver: stubDriver{name: definition.Name},
	}, nil
}

func ensureNoContextCancellationError(err error) bool {
	return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}

func TestSinkDispatcherListSinksHonorsContext(t *testing.T) {
	t.Parallel()

	dispatcher, err := NewSinkDispatcher(nil)
	if err != nil {
		t.Fatalf("new sink dispatcher failed: %v", err)
	}

	ctx, cancel := context.WithDeadline(context.Background(), time.Unix(0, 0))
	cancel()
	_, err = dispatcher.ListSinks(ctx)
	if err == nil {
		t.Fatal("expected context error")
	}
	if ensureNoContextCancellationError(err) {
		t.Fatalf("error = %v, expected context cancellation", err)
	}
}

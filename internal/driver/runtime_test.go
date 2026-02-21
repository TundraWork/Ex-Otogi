package driver

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"ex-otogi/pkg/otogi"
)

func TestRegistryBuildEnabled(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	if err := registry.Register("telegram", func(
		_ context.Context,
		definition Definition,
		_ *slog.Logger,
	) (Runtime, error) {
		if definition.Name == "broken" {
			return Runtime{}, errors.New("broken build")
		}

		return Runtime{
			Source: otogi.EventSource{
				Platform: otogi.PlatformTelegram,
			},
			Driver: stubDriver{name: definition.Name},
		}, nil
	}); err != nil {
		t.Fatalf("register builder failed: %v", err)
	}

	_, err := registry.BuildEnabled(context.Background(), []Definition{
		{Name: "tg-main", Type: "telegram", Enabled: true, Config: []byte("{}")},
		{Name: "broken", Type: "telegram", Enabled: true, Config: []byte("{}")},
	}, slog.Default())
	if err == nil {
		t.Fatal("expected build error")
	}
}

func TestCompositeSinkDispatcherRoutesByID(t *testing.T) {
	t.Parallel()

	primary := &stubSinkDispatcher{}
	secondary := &stubSinkDispatcher{}
	dispatcher, err := NewCompositeSinkDispatcher([]Runtime{
		{
			Source: otogi.EventSource{
				Platform: otogi.PlatformTelegram,
				ID:       "tg-main",
			},
			SinkDispatcher: primary,
		},
		{
			Source: otogi.EventSource{
				Platform: otogi.PlatformTelegram,
				ID:       "tg-alt",
			},
			SinkDispatcher: secondary,
		},
	})
	if err != nil {
		t.Fatalf("new composite sink dispatcher failed: %v", err)
	}

	_, err = dispatcher.SendMessage(context.Background(), otogi.SendMessageRequest{
		Target: otogi.OutboundTarget{
			Conversation: otogi.Conversation{ID: "1", Type: otogi.ConversationTypeGroup},
			Sink: &otogi.EventSink{
				ID: "tg-main",
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

func TestCompositeSinkDispatcherAmbiguousPlatform(t *testing.T) {
	t.Parallel()

	dispatcher, err := NewCompositeSinkDispatcher([]Runtime{
		{
			Source:         otogi.EventSource{Platform: otogi.PlatformTelegram, ID: "tg-main"},
			SinkDispatcher: &stubSinkDispatcher{},
		},
		{
			Source:         otogi.EventSource{Platform: otogi.PlatformTelegram, ID: "tg-alt"},
			SinkDispatcher: &stubSinkDispatcher{},
		},
	})
	if err != nil {
		t.Fatalf("new composite sink dispatcher failed: %v", err)
	}

	_, err = dispatcher.SendMessage(context.Background(), otogi.SendMessageRequest{
		Target: otogi.OutboundTarget{
			Conversation: otogi.Conversation{ID: "1", Type: otogi.ConversationTypeGroup},
			Sink: &otogi.EventSink{
				Platform: otogi.PlatformTelegram,
			},
		},
		Text: "hello",
	})
	if err == nil {
		t.Fatal("expected ambiguity error")
	}
}

func TestCompositeSinkDispatcherListSinksByPlatform(t *testing.T) {
	t.Parallel()

	dispatcher, err := NewCompositeSinkDispatcher([]Runtime{
		{
			Source:         otogi.EventSource{Platform: otogi.PlatformTelegram, ID: "tg-main"},
			SinkDispatcher: &stubSinkDispatcher{},
		},
	})
	if err != nil {
		t.Fatalf("new composite sink dispatcher failed: %v", err)
	}

	sinks, err := dispatcher.ListSinksByPlatform(context.Background(), otogi.PlatformTelegram)
	if err != nil {
		t.Fatalf("list sinks by platform failed: %v", err)
	}
	if len(sinks) != 1 {
		t.Fatalf("sinks len = %d, want 1", len(sinks))
	}
	if sinks[0].ID != "tg-main" {
		t.Fatalf("sink id = %s, want tg-main", sinks[0].ID)
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

func ensureNoContextCancellationError(err error) bool {
	return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}

func TestCompositeSinkDispatcherListSinksHonorsContext(t *testing.T) {
	t.Parallel()

	dispatcher, err := NewCompositeSinkDispatcher(nil)
	if err != nil {
		t.Fatalf("new composite sink dispatcher failed: %v", err)
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

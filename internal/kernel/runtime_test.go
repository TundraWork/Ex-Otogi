package kernel

import (
	"context"
	"testing"

	"ex-otogi/pkg/otogi/platform"
)

func TestWithDefaultSink(t *testing.T) {
	t.Parallel()

	target := withDefaultSink(platform.OutboundTarget{
		Conversation: platform.Conversation{
			ID:   "1",
			Type: platform.ConversationTypeGroup,
		},
	}, &platform.EventSink{
		Platform: platform.PlatformTelegram,
		ID:       "tg-main",
	})
	if target.Sink == nil {
		t.Fatal("sink is nil")
	}
	if target.Sink.ID != "tg-main" {
		t.Fatalf("sink id = %s, want tg-main", target.Sink.ID)
	}
}

func TestModuleServiceRegistryWrapsSinkDispatcher(t *testing.T) {
	t.Parallel()

	services := NewServiceRegistry()
	dispatcher := &captureSinkDispatcher{}
	if err := services.Register(platform.ServiceSinkDispatcher, dispatcher); err != nil {
		t.Fatalf("register sink dispatcher failed: %v", err)
	}

	registry := moduleServiceRegistry{
		base: services,
		defaultSink: &platform.EventSink{
			Platform: platform.PlatformTelegram,
			ID:       "tg-main",
		},
	}
	resolved, err := registry.Resolve(platform.ServiceSinkDispatcher)
	if err != nil {
		t.Fatalf("resolve sink dispatcher failed: %v", err)
	}
	wrapped, ok := resolved.(platform.SinkDispatcher)
	if !ok {
		t.Fatalf("resolved type = %T, want platform.SinkDispatcher", resolved)
	}

	_, err = wrapped.SendMessage(context.Background(), platform.SendMessageRequest{
		Target: platform.OutboundTarget{
			Conversation: platform.Conversation{ID: "1", Type: platform.ConversationTypeGroup},
		},
		Text: "hello",
	})
	if err != nil {
		t.Fatalf("send message failed: %v", err)
	}
	if dispatcher.lastTarget.Sink == nil {
		t.Fatal("last target sink = nil")
	}
	if dispatcher.lastTarget.Sink.ID != "tg-main" {
		t.Fatalf("sink id = %s, want tg-main", dispatcher.lastTarget.Sink.ID)
	}

	sinks, err := wrapped.ListSinksByPlatform(context.Background(), platform.PlatformTelegram)
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

type captureSinkDispatcher struct {
	lastTarget platform.OutboundTarget
	sinks      []platform.EventSink
}

func (d *captureSinkDispatcher) SendMessage(
	_ context.Context,
	request platform.SendMessageRequest,
) (*platform.OutboundMessage, error) {
	d.lastTarget = request.Target
	return &platform.OutboundMessage{
		ID:     "1",
		Target: request.Target,
	}, nil
}

func (*captureSinkDispatcher) EditMessage(context.Context, platform.EditMessageRequest) error {
	return nil
}

func (*captureSinkDispatcher) DeleteMessage(context.Context, platform.DeleteMessageRequest) error {
	return nil
}

func (*captureSinkDispatcher) SetReaction(context.Context, platform.SetReactionRequest) error {
	return nil
}

func (d *captureSinkDispatcher) ListSinks(context.Context) ([]platform.EventSink, error) {
	if len(d.sinks) == 0 {
		return []platform.EventSink{{Platform: platform.PlatformTelegram, ID: "tg-main"}}, nil
	}

	return append([]platform.EventSink(nil), d.sinks...), nil
}

func (d *captureSinkDispatcher) ListSinksByPlatform(
	ctx context.Context,
	targetPlatform platform.Platform,
) ([]platform.EventSink, error) {
	sinks, err := d.ListSinks(ctx)
	if err != nil {
		return nil, err
	}

	filtered := make([]platform.EventSink, 0, len(sinks))
	for _, sink := range sinks {
		if sink.Platform == targetPlatform {
			filtered = append(filtered, sink)
		}
	}

	return filtered, nil
}

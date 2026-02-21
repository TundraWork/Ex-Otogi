package kernel

import (
	"context"
	"testing"

	"ex-otogi/pkg/otogi"
)

func TestWithDefaultSink(t *testing.T) {
	t.Parallel()

	target := withDefaultSink(otogi.OutboundTarget{
		Conversation: otogi.Conversation{
			ID:   "1",
			Type: otogi.ConversationTypeGroup,
		},
	}, &otogi.EventSink{
		Platform: otogi.PlatformTelegram,
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
	if err := services.Register(otogi.ServiceSinkDispatcher, dispatcher); err != nil {
		t.Fatalf("register sink dispatcher failed: %v", err)
	}

	registry := moduleServiceRegistry{
		base: services,
		defaultSink: &otogi.EventSink{
			Platform: otogi.PlatformTelegram,
			ID:       "tg-main",
		},
	}
	resolved, err := registry.Resolve(otogi.ServiceSinkDispatcher)
	if err != nil {
		t.Fatalf("resolve sink dispatcher failed: %v", err)
	}
	wrapped, ok := resolved.(otogi.SinkDispatcher)
	if !ok {
		t.Fatalf("resolved type = %T, want otogi.SinkDispatcher", resolved)
	}

	_, err = wrapped.SendMessage(context.Background(), otogi.SendMessageRequest{
		Target: otogi.OutboundTarget{
			Conversation: otogi.Conversation{ID: "1", Type: otogi.ConversationTypeGroup},
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
}

type captureSinkDispatcher struct {
	lastTarget otogi.OutboundTarget
}

func (d *captureSinkDispatcher) SendMessage(
	_ context.Context,
	request otogi.SendMessageRequest,
) (*otogi.OutboundMessage, error) {
	d.lastTarget = request.Target
	return &otogi.OutboundMessage{
		ID:     "1",
		Target: request.Target,
	}, nil
}

func (*captureSinkDispatcher) EditMessage(context.Context, otogi.EditMessageRequest) error {
	return nil
}

func (*captureSinkDispatcher) DeleteMessage(context.Context, otogi.DeleteMessageRequest) error {
	return nil
}

func (*captureSinkDispatcher) SetReaction(context.Context, otogi.SetReactionRequest) error {
	return nil
}

package telegram

import (
	"context"
	"testing"
	"time"

	"ex-otogi/pkg/otogi/platform"
)

func TestDriverHandleUpdateSkipsNilDecodedEvent(t *testing.T) {
	t.Parallel()

	driver, err := NewDriver(NoopSource{}, driverDecoderStub{})
	if err != nil {
		t.Fatalf("new driver failed: %v", err)
	}

	sink := &driverSinkSpy{}
	if err := driver.handleUpdate(context.Background(), Update{Type: UpdateTypeMessage}, sink); err != nil {
		t.Fatalf("handle update failed: %v", err)
	}
	if sink.publishCalls != 0 {
		t.Fatalf("publish calls = %d, want 0", sink.publishCalls)
	}
}

func TestDriverHandleUpdatePublishesDecodedEvent(t *testing.T) {
	t.Parallel()

	driver, err := NewDriver(NoopSource{}, driverDecoderStub{
		event: &platform.Event{
			ID:         "event-1",
			Kind:       platform.EventKindArticleCreated,
			OccurredAt: time.Unix(1_700_000_000, 0).UTC(),
			Conversation: platform.Conversation{
				ID:   "chat-1",
				Type: platform.ConversationTypeGroup,
			},
			Actor:   platform.Actor{ID: "user-1"},
			Article: &platform.Article{ID: "article-1", Text: "hello"},
		},
	})
	if err != nil {
		t.Fatalf("new driver failed: %v", err)
	}

	sink := &driverSinkSpy{}
	if err := driver.handleUpdate(context.Background(), Update{Type: UpdateTypeMessage}, sink); err != nil {
		t.Fatalf("handle update failed: %v", err)
	}
	if sink.publishCalls != 1 {
		t.Fatalf("publish calls = %d, want 1", sink.publishCalls)
	}
	if sink.lastEvent == nil {
		t.Fatal("published event is nil")
	}
	if sink.lastEvent.Source.Platform != DriverPlatform {
		t.Fatalf("source platform = %s, want %s", sink.lastEvent.Source.Platform, DriverPlatform)
	}
	if sink.lastEvent.Source.ID != DriverType {
		t.Fatalf("source id = %s, want %s", sink.lastEvent.Source.ID, DriverType)
	}
}

type driverDecoderStub struct {
	event *platform.Event
	err   error
}

func (d driverDecoderStub) Decode(_ context.Context, _ Update) (*platform.Event, error) {
	if d.err != nil {
		return nil, d.err
	}

	return d.event, nil
}

type driverSinkSpy struct {
	publishCalls int
	lastEvent    *platform.Event
}

func (s *driverSinkSpy) Publish(_ context.Context, event *platform.Event) error {
	s.publishCalls++
	s.lastEvent = event

	return nil
}

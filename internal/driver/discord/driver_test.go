package discord

import (
	"context"
	"errors"
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
				ID:   "channel-1",
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

func TestDriverHandleUpdateRecoversPanic(t *testing.T) {
	t.Parallel()

	driver, err := NewDriver(NoopSource{}, driverDecoderStub{panicValue: "test panic"})
	if err != nil {
		t.Fatalf("new driver failed: %v", err)
	}

	var asyncErr error
	driver.cfg.onAsyncError = func(_ context.Context, err error) {
		asyncErr = err
	}

	sink := &driverSinkSpy{}
	err = driver.handleUpdate(context.Background(), Update{Type: UpdateTypeMessage}, sink)
	if err == nil {
		t.Fatal("expected error from panic recovery")
	}
	if sink.publishCalls != 0 {
		t.Fatalf("publish calls = %d, want 0", sink.publishCalls)
	}
	if asyncErr == nil {
		t.Fatal("expected async error callback")
	}
}

func TestDriverStartReturnsNilOnContextCancellation(t *testing.T) {
	t.Parallel()

	driver, err := NewDriver(NoopSource{}, driverDecoderStub{})
	if err != nil {
		t.Fatalf("new driver failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	sink := &driverSinkSpy{}
	if err := driver.Start(ctx, sink); err != nil {
		t.Fatalf("start returned error: %v", err)
	}
}

func TestDriverStartRejectsNilSink(t *testing.T) {
	t.Parallel()

	driver, err := NewDriver(NoopSource{}, driverDecoderStub{})
	if err != nil {
		t.Fatalf("new driver failed: %v", err)
	}

	if err := driver.Start(context.Background(), nil); err == nil {
		t.Fatal("expected error for nil sink")
	}
}

func TestDriverStartPropagatesSourceError(t *testing.T) {
	t.Parallel()

	sourceErr := errors.New("gateway disconnected")
	driver, err := NewDriver(errorSource{err: sourceErr}, driverDecoderStub{})
	if err != nil {
		t.Fatalf("new driver failed: %v", err)
	}

	sink := &driverSinkSpy{}
	err = driver.Start(context.Background(), sink)
	if err == nil {
		t.Fatal("expected error from source")
	}
	if !errors.Is(err, sourceErr) {
		t.Fatalf("error = %v, want wrapping %v", err, sourceErr)
	}
}

func TestNewDriverRejectsNilSource(t *testing.T) {
	t.Parallel()

	_, err := NewDriver(nil, driverDecoderStub{})
	if err == nil {
		t.Fatal("expected error for nil source")
	}
}

func TestNewDriverRejectsNilDecoder(t *testing.T) {
	t.Parallel()

	_, err := NewDriver(NoopSource{}, nil)
	if err == nil {
		t.Fatal("expected error for nil decoder")
	}
}

func TestDriverName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		options  []DriverOption
		expected string
	}{
		{
			name:     "default name",
			options:  nil,
			expected: DriverType,
		},
		{
			name:     "custom name",
			options:  []DriverOption{WithName("discord-prod")},
			expected: "discord-prod",
		},
		{
			name:     "empty name ignored",
			options:  []DriverOption{WithName("")},
			expected: DriverType,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			driver, err := NewDriver(NoopSource{}, driverDecoderStub{}, testCase.options...)
			if err != nil {
				t.Fatalf("new driver failed: %v", err)
			}
			if driver.Name() != testCase.expected {
				t.Fatalf("name = %s, want %s", driver.Name(), testCase.expected)
			}
		})
	}
}

func TestDriverShutdown(t *testing.T) {
	t.Parallel()

	driver, err := NewDriver(NoopSource{}, driverDecoderStub{})
	if err != nil {
		t.Fatalf("new driver failed: %v", err)
	}

	if err := driver.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown returned error: %v", err)
	}
}

func TestChannelSourceConsumesUpdates(t *testing.T) {
	t.Parallel()

	updates := make(chan Update, 2)
	updates <- Update{ID: "u1", Type: UpdateTypeMessage}
	updates <- Update{ID: "u2", Type: UpdateTypeEdit}
	close(updates)

	source := ChannelSource{Updates: updates}
	var received []string
	handler := func(_ context.Context, update Update) error {
		received = append(received, update.ID)
		return nil
	}

	if err := source.Consume(context.Background(), handler); err != nil {
		t.Fatalf("consume failed: %v", err)
	}
	if len(received) != 2 {
		t.Fatalf("received = %d updates, want 2", len(received))
	}
	if received[0] != "u1" || received[1] != "u2" {
		t.Fatalf("received = %v, want [u1, u2]", received)
	}
}

func TestChannelSourceRejectsNilHandler(t *testing.T) {
	t.Parallel()

	source := ChannelSource{Updates: make(chan Update)}
	if err := source.Consume(context.Background(), nil); err == nil {
		t.Fatal("expected error for nil handler")
	}
}

func TestChannelSourceStopsOnContextCancellation(t *testing.T) {
	t.Parallel()

	updates := make(chan Update)
	source := ChannelSource{Updates: updates}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	handler := func(_ context.Context, _ Update) error { return nil }
	if err := source.Consume(ctx, handler); err != nil {
		t.Fatalf("consume returned error: %v", err)
	}
}

// --- test doubles ---

type driverDecoderStub struct {
	event      *platform.Event
	err        error
	panicValue interface{}
}

func (d driverDecoderStub) Decode(_ context.Context, _ Update) (*platform.Event, error) {
	if d.panicValue != nil {
		panic(d.panicValue)
	}
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

type errorSource struct {
	err error
}

func (s errorSource) Consume(_ context.Context, _ UpdateHandler) error {
	return s.err
}

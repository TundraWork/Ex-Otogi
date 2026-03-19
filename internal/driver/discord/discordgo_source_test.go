package discord

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

// discordgoTestSession is a controllable mock of DiscordgoSessionClient.
type discordgoTestSession struct {
	mu       sync.Mutex
	handlers []interface{}
	openFn   func() error
	closeFn  func() error
}

func (s *discordgoTestSession) Open() error {
	if s.openFn != nil {
		return s.openFn()
	}

	return nil
}

func (s *discordgoTestSession) Close() error {
	if s.closeFn != nil {
		return s.closeFn()
	}

	return nil
}

func (s *discordgoTestSession) AddHandler(h interface{}) func() {
	s.mu.Lock()
	s.handlers = append(s.handlers, h)
	s.mu.Unlock()

	return func() {}
}

// fireMessageCreate dispatches a MessageCreate event to all registered handlers.
func (s *discordgoTestSession) fireMessageCreate(evt *discordgo.MessageCreate) {
	s.mu.Lock()
	handlers := s.handlers
	s.mu.Unlock()

	for _, h := range handlers {
		if fn, ok := h.(func(*discordgo.Session, *discordgo.MessageCreate)); ok {
			fn(nil, evt)
		}
	}
}

// discordgoTestMapper is a controllable mock of DiscordgoEventMapper.
type discordgoTestMapper struct {
	result   Update
	accepted bool
	err      error
}

func (m *discordgoTestMapper) Map(_ context.Context, _ any) (Update, bool, error) {
	return m.result, m.accepted, m.err
}

func TestDiscordgoSourceNew(t *testing.T) {
	t.Parallel()

	mapper := &discordgoTestMapper{}
	session := &discordgoTestSession{}

	tests := []struct {
		name          string
		client        DiscordgoSessionClient
		mapper        DiscordgoEventMapper
		buffer        int
		wantErr       bool
		wantErrSubstr string
	}{
		{
			name:          "nil client returns error",
			client:        nil,
			mapper:        mapper,
			wantErr:       true,
			wantErrSubstr: "nil client",
		},
		{
			name:          "nil mapper returns error",
			client:        session,
			mapper:        nil,
			wantErr:       true,
			wantErrSubstr: "nil mapper",
		},
		{
			name:    "zero buffer uses default",
			client:  session,
			mapper:  mapper,
			buffer:  0,
			wantErr: false,
		},
		{
			name:    "valid config succeeds",
			client:  session,
			mapper:  mapper,
			buffer:  64,
			wantErr: false,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			src, err := NewDiscordgoSource(testCase.client, testCase.mapper, testCase.buffer)
			if testCase.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if testCase.wantErrSubstr != "" && !containsStr(err.Error(), testCase.wantErrSubstr) {
					t.Errorf("error %q does not contain %q", err.Error(), testCase.wantErrSubstr)
				}

				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if src == nil {
				t.Fatal("expected non-nil source")
			}
		})
	}
}

func TestDiscordgoSourceConsumeNilHandler(t *testing.T) {
	t.Parallel()

	src, err := NewDiscordgoSource(&discordgoTestSession{}, &discordgoTestMapper{}, 10)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	err = src.Consume(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for nil handler")
	}
	if !containsStr(err.Error(), "nil handler") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestDiscordgoSourceConsumeOpenError(t *testing.T) {
	t.Parallel()

	openErr := errors.New("gateway connect failed")
	session := &discordgoTestSession{
		openFn: func() error { return openErr },
	}

	src, err := NewDiscordgoSource(session, &discordgoTestMapper{}, 10)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	err = src.Consume(context.Background(), func(_ context.Context, _ Update) error { return nil })
	if err == nil {
		t.Fatal("expected error from Open()")
	}
	if !errors.Is(err, openErr) {
		t.Errorf("expected wrapped openErr, got: %v", err)
	}
}

func TestDiscordgoSourceConsumeContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	src, err := NewDiscordgoSource(&discordgoTestSession{}, &discordgoTestMapper{}, 10)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	err = src.Consume(ctx, func(_ context.Context, _ Update) error { return nil })
	if err != nil {
		t.Errorf("expected nil error on context cancellation, got: %v", err)
	}
}

func TestDiscordgoSourceConsumeMapperError(t *testing.T) {
	t.Parallel()

	mapErr := errors.New("map failed")
	mapper := &discordgoTestMapper{err: mapErr}

	session := &discordgoTestSession{
		openFn: func() error { return nil },
	}

	src, err := NewDiscordgoSource(session, mapper, 10)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	// Inject an event via goroutine after Consume starts.
	go func() {
		time.Sleep(10 * time.Millisecond)
		session.fireMessageCreate(&discordgo.MessageCreate{
			Message: &discordgo.Message{ID: "m1", ChannelID: "c1"},
		})
	}()

	err = src.Consume(context.Background(), func(_ context.Context, _ Update) error { return nil })
	if err == nil {
		t.Fatal("expected error from mapper")
	}
	if !errors.Is(err, mapErr) {
		t.Errorf("expected wrapped mapErr, got: %v", err)
	}
}

func TestDiscordgoSourceConsumeHandlerError(t *testing.T) {
	t.Parallel()

	handlerErr := errors.New("handler failed")
	mapper := &discordgoTestMapper{
		result:   Update{Type: UpdateTypeMessage},
		accepted: true,
	}
	session := &discordgoTestSession{}

	src, err := NewDiscordgoSource(session, mapper, 10)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	go func() {
		time.Sleep(10 * time.Millisecond)
		session.fireMessageCreate(&discordgo.MessageCreate{
			Message: &discordgo.Message{ID: "m1", ChannelID: "c1"},
		})
	}()

	err = src.Consume(context.Background(), func(_ context.Context, _ Update) error {
		return handlerErr
	})
	if err == nil {
		t.Fatal("expected error from handler")
	}
	if !errors.Is(err, handlerErr) {
		t.Errorf("expected wrapped handlerErr, got: %v", err)
	}
}

func TestDiscordgoSourceConsumeEventFlow(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	testMsg := &discordgo.MessageCreate{
		Message: &discordgo.Message{
			ID:        "msg-1",
			ChannelID: "chan-1",
			Content:   "hello",
		},
	}

	var mappedRaw any
	mapper := &discordgoTestMapper{}
	mapper.err = nil
	mapper.accepted = true
	mapper.result = Update{Type: UpdateTypeMessage}

	captureMapper := &capturingDiscordgoMapper{
		inner: mapper,
		onMap: func(raw any) {
			mappedRaw = raw
		},
	}

	session := &discordgoTestSession{}
	src, err := NewDiscordgoSource(session, captureMapper, 10)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	var handlerCalled bool
	handlerDone := make(chan struct{})
	handler := func(_ context.Context, _ Update) error {
		handlerCalled = true
		cancel()
		close(handlerDone)

		return nil
	}

	go func() {
		time.Sleep(10 * time.Millisecond)
		session.fireMessageCreate(testMsg)
	}()

	if err := src.Consume(ctx, handler); err != nil {
		t.Fatalf("consume returned unexpected error: %v", err)
	}

	select {
	case <-handlerDone:
	case <-time.After(1 * time.Second):
		t.Fatal("handler was not called within timeout")
	}

	if !handlerCalled {
		t.Error("handler was not called")
	}
	if mappedRaw != testMsg {
		t.Errorf("mapper received wrong event: got %v, want %v", mappedRaw, testMsg)
	}
}

// capturingDiscordgoMapper wraps a mapper and records the raw event received.
type capturingDiscordgoMapper struct {
	inner DiscordgoEventMapper
	onMap func(raw any)
}

func (c *capturingDiscordgoMapper) Map(ctx context.Context, raw any) (Update, bool, error) {
	if c.onMap != nil {
		c.onMap(raw)
	}

	update, accepted, err := c.inner.Map(ctx, raw)
	if err != nil {
		return Update{}, false, fmt.Errorf("capturing mapper: %w", err)
	}

	return update, accepted, nil
}

// containsStr is a helper to check if a string contains a substring.
func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}

			return false
		}())
}

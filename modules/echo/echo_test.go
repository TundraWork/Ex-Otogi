package echo

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

func TestHandleEchoesInputThrough4090Bytes(t *testing.T) {
	t.Parallel()
	module := New()
	invocation := &recordingInvocation{}

	result := module.Handle(context.Background(), fakeEvent{text: mustText(t, strings.Repeat("a", 4090))}, invocation)

	if result.Status() != otogi.ResultSucceeded || result.Err() != nil {
		t.Fatalf("result = (%s, %v), want succeeded", result.Status(), result.Err())
	}
	if invocation.slot != "echo" || invocation.text.String() != "echo: "+strings.Repeat("a", 4090) {
		t.Fatalf("SendText = (%q, %d bytes), want echo slot and 4096-byte echo", invocation.slot, len(invocation.text.String()))
	}
}

func TestHandleFailsWithoutSendingFor4091Bytes(t *testing.T) {
	t.Parallel()
	module := New()
	invocation := &recordingInvocation{}

	result := module.Handle(context.Background(), fakeEvent{text: mustText(t, strings.Repeat("a", 4091))}, invocation)

	if result.Status() != otogi.ResultFailed || !errors.Is(result.Err(), otogi.ErrInvalid) {
		t.Fatalf("result = (%s, %v), want failed ErrInvalid", result.Status(), result.Err())
	}
	if invocation.calls != 0 {
		t.Fatalf("SendText calls = %d, want 0", invocation.calls)
	}
}

func TestHandleMapsCancellationAndSendFailure(t *testing.T) {
	t.Parallel()
	module := New()
	tests := []struct {
		name       string
		ctx        context.Context
		sendError  error
		wantStatus otogi.ResultStatus
		wantError  error
	}{
		{name: "canceled context", ctx: canceledContext(), wantStatus: otogi.ResultCanceled, wantError: context.Canceled},
		{name: "canceled send", ctx: context.Background(), sendError: context.Canceled, wantStatus: otogi.ResultCanceled, wantError: context.Canceled},
		{name: "failed send", ctx: context.Background(), sendError: otogi.ErrUnavailable, wantStatus: otogi.ResultFailed, wantError: otogi.ErrUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			invocation := &recordingInvocation{err: tt.sendError}
			result := module.Handle(tt.ctx, fakeEvent{text: mustText(t, "hello")}, invocation)
			if result.Status() != tt.wantStatus || !errors.Is(result.Err(), tt.wantError) {
				t.Fatalf("result = (%s, %v), want (%s, %v)", result.Status(), result.Err(), tt.wantStatus, tt.wantError)
			}
		})
	}
}

func TestDescriptorConsumesTextMessage(t *testing.T) {
	t.Parallel()
	descriptor := New().Descriptor()
	if descriptor.ID().String() != "echo" {
		t.Fatalf("ID = %q, want echo", descriptor.ID())
	}
	consumes := descriptor.Consumes()
	if len(consumes) != 1 || consumes[0].String() != "text.message" {
		t.Fatalf("Consumes = %v, want text.message", consumes)
	}
}

type recordingInvocation struct {
	calls int
	slot  string
	text  otogi.Text
	err   error
}

func (i *recordingInvocation) SendText(_ context.Context, slot string, text otogi.Text) error {
	i.calls++
	i.slot = slot
	i.text = text
	return i.err
}

type fakeEvent struct{ text otogi.Text }

func (e fakeEvent) ID() otogi.EventID                          { return otogi.EventID{} }
func (e fakeEvent) Platform() otogi.PlatformID                 { return otogi.PlatformID{} }
func (e fakeEvent) Account() otogi.AccountID                   { return otogi.AccountID{} }
func (e fakeEvent) Tenant() otogi.TenantID                     { return otogi.TenantID{} }
func (e fakeEvent) Conversation() otogi.ConversationID         { return otogi.ConversationID{} }
func (e fakeEvent) SourceEventID() (otogi.SourceEventID, bool) { return otogi.SourceEventID{}, false }
func (e fakeEvent) Actor() (otogi.ActorID, bool)               { return otogi.ActorID{}, false }
func (e fakeEvent) Kind() otogi.EventKind                      { return otogi.EventKind{} }
func (e fakeEvent) SchemaVersion() uint64                      { return 1 }
func (e fakeEvent) OccurredAt() time.Time                      { return time.Time{} }
func (e fakeEvent) IngestedAt() time.Time                      { return time.Time{} }
func (e fakeEvent) TraceID() (otogi.TraceID, bool)             { return otogi.TraceID{}, false }
func (e fakeEvent) CausalEventID() (otogi.CausalEventID, bool) { return otogi.CausalEventID{}, false }
func (e fakeEvent) Text() otogi.Text                           { return e.text }

func mustText(t *testing.T, value string) otogi.Text {
	t.Helper()
	text, err := otogi.NewText(value)
	if err != nil {
		t.Fatalf("NewText: %v", err)
	}
	return text
}

func canceledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

package echo

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

func TestHandleEchoesInputAtMaximumOutputSize(t *testing.T) {
	t.Parallel()
	module := New()
	invocation := &recordingInvocation{}
	input := strings.Repeat("a", 64*1024-len(prefix))
	result := module.Handle(context.Background(), fakeEvent{message: mustMessage(t, input)}, invocation)
	if result.Status() != otogi.ResultSucceeded || result.Err() != nil {
		t.Fatalf("result = (%s, %v), want succeeded", result.Status(), result.Err())
	}
	text, _, _ := invocation.message.Parts()[0].Text()
	if invocation.slot != "echo" || text != prefix+input {
		t.Fatalf("Send = (%q, %d bytes), want echo slot and maximum text", invocation.slot, len(text))
	}
}

func TestHandleRejectsOversizedEchoWithoutSending(t *testing.T) {
	t.Parallel()
	module := New()
	invocation := &recordingInvocation{}
	result := module.Handle(context.Background(), fakeEvent{message: mustMessage(t, strings.Repeat("a", 64*1024-len(prefix)+1))}, invocation)
	if result.Status() != otogi.ResultRejected || result.Err() != nil {
		t.Fatalf("result = (%s, %v), want rejected", result.Status(), result.Err())
	}
	if invocation.calls != 0 {
		t.Fatalf("Send calls = %d, want 0", invocation.calls)
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
			result := module.Handle(tt.ctx, fakeEvent{message: mustMessage(t, "hello")}, invocation)
			if result.Status() != tt.wantStatus || !errors.Is(result.Err(), tt.wantError) {
				t.Fatalf("result = (%s, %v), want (%s, %v)", result.Status(), result.Err(), tt.wantStatus, tt.wantError)
			}
		})
	}
}

func TestDescriptorConsumesMessageCreated(t *testing.T) {
	t.Parallel()
	descriptor := New().Descriptor()
	if descriptor.ID().String() != "echo" {
		t.Fatalf("ID = %q, want echo", descriptor.ID())
	}
	consumes := descriptor.Consumes()
	if len(consumes) != 1 || consumes[0] != otogi.ActionMessageCreated {
		t.Fatalf("Consumes = %v, want message.created", consumes)
	}
}

type recordingInvocation struct {
	calls   int
	slot    string
	message otogi.Message
	err     error
}

func (i *recordingInvocation) Send(_ context.Context, slot string, message otogi.Message) error {
	i.calls++
	i.slot = slot
	i.message = message
	return i.err
}

type fakeEvent struct{ message otogi.Message }

func (e fakeEvent) ID() otogi.EventID                          { return otogi.EventID{} }
func (e fakeEvent) Platform() otogi.PlatformID                 { return otogi.PlatformID{} }
func (e fakeEvent) Account() otogi.AccountID                   { return otogi.AccountID{} }
func (e fakeEvent) Tenant() otogi.TenantID                     { return otogi.TenantID{} }
func (e fakeEvent) Conversation() otogi.ConversationID         { return otogi.ConversationID{} }
func (e fakeEvent) SourceEventID() (otogi.SourceEventID, bool) { return otogi.SourceEventID{}, false }
func (e fakeEvent) Actor() (otogi.ActorID, bool)               { return otogi.ActorID{}, false }
func (e fakeEvent) Action() otogi.EventAction                  { return otogi.ActionMessageCreated }
func (e fakeEvent) Direction() otogi.Direction                 { return otogi.DirectionIncoming }
func (e fakeEvent) OccurredAt() time.Time                      { return time.Time{} }
func (e fakeEvent) IngestedAt() time.Time                      { return time.Time{} }
func (e fakeEvent) EditedAt() (time.Time, bool)                { return time.Time{}, false }
func (e fakeEvent) TraceID() (otogi.TraceID, bool)             { return otogi.TraceID{}, false }
func (e fakeEvent) CausalEventID() (otogi.CausalEventID, bool) { return otogi.CausalEventID{}, false }
func (e fakeEvent) Completeness() otogi.Completeness           { return otogi.CompletenessComplete }
func (e fakeEvent) ProviderLocator() (otogi.ProviderLocator, bool) {
	return otogi.ProviderLocator{}, false
}
func (e fakeEvent) Message() otogi.Message        { return e.message }
func (e fakeEvent) Extensions() []otogi.Extension { return nil }

func mustMessage(t *testing.T, value string) otogi.Message {
	t.Helper()
	id, _ := otogi.NewPartID("text")
	part, err := otogi.NewTextPart(id, value, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	message, err := otogi.NewMessage([]otogi.PartID{id}, []otogi.Part{part}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return message
}

func canceledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

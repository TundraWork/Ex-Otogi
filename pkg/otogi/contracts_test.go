package otogi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math"
	"sync"
	"testing"
	"time"
)

func mustValue[T any](t *testing.T, newValue func(string) (T, error), value string) T {
	t.Helper()
	got, err := newValue(value)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func validEventInputParams(t *testing.T) EventInputParams {
	t.Helper()
	return EventInputParams{
		Platform: mustValue(t, NewPlatformID, "platform"), Account: mustValue(t, NewAccountID, "account"),
		Tenant: mustValue(t, NewTenantID, "tenant"), Conversation: mustValue(t, NewConversationID, "conversation"),
		Action: ActionMessageCreated, Direction: DirectionIncoming, Completeness: CompletenessComplete,
		OccurredAt: time.Date(2026, 8, 27, 12, 0, 0, 0, time.FixedZone("local", 3600)),
		IngestedAt: time.Date(2026, 8, 27, 12, 0, 1, 0, time.FixedZone("local", 3600)),
		Message:    testRichMessage(t, "hello"),
	}
}

func testRichMessage(t *testing.T, text string) Message {
	t.Helper()
	id := mustValue(t, NewPartID, "part")
	part, err := NewTextPart(id, text, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := NewMessage([]PartID{id}, []Part{part}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return msg
}

func TestEventInputValidatesAndCanonicalizes(t *testing.T) {
	p := validEventInputParams(t)
	p.SourceEventID = mustValue(t, NewSourceEventID, "source")
	p.Actor = mustValue(t, NewActorID, "actor")
	p.TraceID = mustValue(t, NewTraceID, "trace")
	p.CausalEventID = mustValue(t, NewCausalEventID, "cause")
	v, err := NewEventInput(p)
	if err != nil {
		t.Fatal(err)
	}
	if v.Platform() != p.Platform || v.Account() != p.Account || v.Tenant() != p.Tenant || v.Conversation() != p.Conversation || v.Action() != p.Action || v.Direction() != p.Direction || v.Completeness() != p.Completeness {
		t.Fatal("required accessors differ")
	}
	if got, ok := v.SourceEventID(); !ok || got != p.SourceEventID {
		t.Fatal("source event differs")
	}
	if got, ok := v.Actor(); !ok || got != p.Actor {
		t.Fatal("actor differs")
	}
	if got, ok := v.TraceID(); !ok || got != p.TraceID {
		t.Fatal("trace differs")
	}
	if got, ok := v.CausalEventID(); !ok || got != p.CausalEventID {
		t.Fatal("causal event differs")
	}
	if v.OccurredAt().Location() != time.UTC || v.IngestedAt().Location() != time.UTC {
		t.Fatal("timestamps are not UTC")
	}
}
func TestEventInputOptionalFieldsAreAbsent(t *testing.T) {
	empty, err := NewEventInput(validEventInputParams(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := empty.SourceEventID(); ok {
		t.Fatal("unexpected source event")
	}
	if _, ok := empty.Actor(); ok {
		t.Fatal("unexpected actor")
	}
	if _, ok := empty.TraceID(); ok {
		t.Fatal("unexpected trace")
	}
	if _, ok := empty.CausalEventID(); ok {
		t.Fatal("unexpected causal event")
	}
}

func TestEventInputRejectsInvalidRequiredFields(t *testing.T) {
	mutations := []func(*EventInputParams){
		func(p *EventInputParams) { p.Platform = PlatformID{} }, func(p *EventInputParams) { p.Account = AccountID{} },
		func(p *EventInputParams) { p.Tenant = TenantID{} }, func(p *EventInputParams) { p.Conversation = ConversationID{} },
		func(p *EventInputParams) { p.Action = EventAction{} }, func(p *EventInputParams) { p.Direction = Direction("") },
		func(p *EventInputParams) { p.OccurredAt = time.Time{} }, func(p *EventInputParams) { p.IngestedAt = time.Time{} },
		func(p *EventInputParams) { p.OccurredAt = p.IngestedAt.Add(time.Second) }, func(p *EventInputParams) { p.Message = Message{} },
	}
	for i, mutate := range mutations {
		p := validEventInputParams(t)
		mutate(&p)
		if _, err := NewEventInput(p); !errors.Is(err, ErrInvalid) {
			t.Fatalf("case %d: %v", i, err)
		}
	}
}

func TestModuleDescriptorOwnsConsumedKinds(t *testing.T) {
	id := mustValue(t, NewModuleID, "echo")
	first := ActionMessageCreated
	second := ActionMessageEdited
	input := []EventAction{first}
	d, err := NewModuleDescriptor(id, input)
	if err != nil {
		t.Fatal(err)
	}
	input[0] = second
	got := d.Consumes()
	if d.ID() != id || len(got) != 1 || got[0] != first {
		t.Fatalf("descriptor changed: %#v", got)
	}
	got[0] = second
	if d.Consumes()[0] != first {
		t.Fatal("Consumes exposed internal storage")
	}
	if _, err := NewModuleDescriptor(id, []EventAction{first, first}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate: %v", err)
	}
	if _, err := NewModuleDescriptor(ModuleID{}, []EventAction{first}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("zero ID: %v", err)
	}
	if _, err := NewModuleDescriptor(id, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty kinds: %v", err)
	}
}

func TestResultInvariants(t *testing.T) {
	boom := errors.New("boom")
	valid := []struct {
		status ResultStatus
		err    error
	}{{ResultSucceeded, nil}, {ResultRejected, nil}, {ResultFailed, boom}, {ResultCanceled, boom}}
	for _, tt := range valid {
		r, err := NewResult(tt.status, tt.err)
		if err != nil || r.Status() != tt.status || r.Err() != tt.err {
			t.Fatalf("%s: %#v, %v", tt.status, r, err)
		}
	}
	invalid := []struct {
		status ResultStatus
		err    error
	}{{ResultStatus("unknown"), nil}, {ResultStatus("unknown"), boom}, {ResultSucceeded, boom}, {ResultRejected, boom}, {ResultFailed, nil}, {ResultCanceled, nil}}
	for _, tt := range invalid {
		if _, err := NewResult(tt.status, tt.err); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%q/%v: %v", tt.status, tt.err, err)
		}
	}
	if NewSucceededResult().Status() != ResultSucceeded || NewRejectedResult().Status() != ResultRejected || NewFailedResult(boom).Err() != boom || NewCanceledResult(boom).Err() != boom {
		t.Fatal("convenience constructor mismatch")
	}
	if !errors.Is(NewFailedResult(nil).Err(), ErrInvalid) || !errors.Is(NewCanceledResult(nil).Err(), ErrInvalid) {
		t.Fatal("nil error convenience constructors must preserve invariant")
	}
}

func TestOutboundMessageIsImmutable(t *testing.T) {
	p := OutboundMessageParams{Tenant: mustValue(t, NewTenantID, "tenant"), Platform: mustValue(t, NewPlatformID, "platform"), Account: mustValue(t, NewAccountID, "account"), Conversation: mustValue(t, NewConversationID, "conversation"), OriginatingEvent: mustValue(t, NewEventID, "event"), OriginatingModule: mustValue(t, NewModuleID, "module"), Message: testRichMessage(t, "hello"), IdempotencyKey: mustValue(t, NewIdempotencyKey, "key")}
	m, err := NewOutboundMessage(p)
	if err != nil {
		t.Fatal(err)
	}
	p.Message = testRichMessage(t, "changed")
	text, _, _ := m.Message().Parts()[0].Text()
	if m.Tenant().String() != "tenant" || m.Platform().String() != "platform" || m.Account().String() != "account" || m.Conversation().String() != "conversation" || m.OriginatingEvent().String() != "event" || m.OriginatingModule().String() != "module" || text != "hello" || m.IdempotencyKey().String() != "key" {
		t.Fatal("message accessors differ")
	}
	bad := OutboundMessageParams{}
	if _, err := NewOutboundMessage(bad); !errors.Is(err, ErrInvalid) {
		t.Fatalf("zero message: %v", err)
	}
}

func TestMessagingIdempotencyKeyInputsAndEncoding(t *testing.T) {
	event := mustValue(t, NewEventID, "event")
	module := mustValue(t, NewModuleID, "module")
	got, err := newMessagingIdempotencyKey(event, module, "reply")
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	var size [8]byte
	for _, part := range []string{"event", "module", "core.messaging", "reply"} {
		binary.BigEndian.PutUint64(size[:], uint64(len(part)))
		h.Write(size[:])
		h.Write([]byte(part))
	}
	want := hex.EncodeToString(h.Sum(nil))
	if got.String() != want {
		t.Fatalf("got %s, want %s", got, want)
	}
	for _, slot := range []string{"", " reply", "reply ", string([]byte{0xff}), string(bytes.Repeat([]byte{'x'}, 65))} {
		if _, err := newMessagingIdempotencyKey(event, module, slot); !errors.Is(err, ErrInvalid) {
			t.Fatalf("slot %q: %v", slot, err)
		}
	}
	if _, err := newMessagingIdempotencyKey(EventID{}, module, "reply"); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}

func TestEventIDGeneratorSequenceConcurrencyAndOverflow(t *testing.T) {
	g, err := NewEventIDGenerator(bytes.NewReader(make([]byte, 16)))
	if err != nil {
		t.Fatal(err)
	}
	first, err := g.Next()
	if err != nil || first.String() != "evt_000000000000000000000000000000000000000000000001" {
		t.Fatalf("first: %q %v", first, err)
	}
	const n = 100
	ids := make(chan EventID, n)
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, e := g.Next()
			if e != nil {
				t.Error(e)
				return
			}
			ids <- id
		}()
	}
	wg.Wait()
	close(ids)
	seen := map[EventID]bool{}
	for id := range ids {
		if seen[id] {
			t.Fatalf("duplicate %s", id)
		}
		seen[id] = true
	}
	if len(seen) != n {
		t.Fatalf("got %d IDs", len(seen))
	}
	over, err := newEventIDGeneratorWithCounter(bytes.NewReader(make([]byte, 16)), math.MaxUint64-1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = over.Next(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if id, err := over.Next(); !errors.Is(err, ErrInvalidState) || id != (EventID{}) {
			t.Fatalf("overflow: %q %v", id, err)
		}
	}
	if _, err := NewEventIDGenerator(bytes.NewReader(make([]byte, 15))); err == nil {
		t.Fatal("short nonce accepted")
	}
	var zero EventIDGenerator
	if _, err := zero.Next(); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("zero generator: %v", err)
	}
}

var _ Module = moduleContract(nil)
var _ Invocation = invocationContract(nil)
var _ Messenger = messengerContract(nil)

type moduleContract interface {
	Descriptor() ModuleDescriptor
	Handle(context.Context, Event, Invocation) Result
}
type invocationContract interface {
	Send(context.Context, string, Message) error
}
type messengerContract interface {
	Send(context.Context, OutboundMessage) error
}

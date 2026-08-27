package scripted

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
	"go.uber.org/goleak"
)

func TestSourceCopiesInputsAndSubmitsSerially(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	first := testInput(t, "first")
	second := testInput(t, "second")
	inputs := []otogi.EventInput{first, second}
	source := New(inputs)
	inputs[0] = testInput(t, "changed")

	sink := &recordingSink{}
	if err := source.Run(context.Background(), sink); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(sink.inputs) != 2 || sink.inputs[0].Text().String() != "first" || sink.inputs[1].Text().String() != "second" {
		t.Fatalf("submitted texts = %v, want [first second]", inputTexts(sink.inputs))
	}
}

func TestSourcePropagatesCancellationAndStops(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	sink := &recordingSink{afterSubmit: cancel}
	source := New([]otogi.EventInput{testInput(t, "first"), testInput(t, "second")})

	err := source.Run(ctx, sink)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
	if len(sink.inputs) != 1 {
		t.Fatalf("Submit calls = %d, want 1", len(sink.inputs))
	}
}

func TestSourceReturnsSinkErrorWithoutSubmittingLaterInputs(t *testing.T) {
	t.Parallel()
	want := errors.New("submit failed")
	sink := &recordingSink{err: want}
	source := New([]otogi.EventInput{testInput(t, "first"), testInput(t, "second")})

	if err := source.Run(context.Background(), sink); !errors.Is(err, want) {
		t.Fatalf("Run error = %v, want %v", err, want)
	}
	if len(sink.inputs) != 1 {
		t.Fatalf("Submit calls = %d, want 1", len(sink.inputs))
	}
}

type recordingSink struct {
	inputs      []otogi.EventInput
	err         error
	afterSubmit func()
}

func (s *recordingSink) Submit(_ context.Context, input otogi.EventInput) error {
	s.inputs = append(s.inputs, input)
	if s.afterSubmit != nil {
		s.afterSubmit()
	}
	return s.err
}

func testInput(t *testing.T, value string) otogi.EventInput {
	t.Helper()
	platform, _ := otogi.NewPlatformID("platform")
	account, _ := otogi.NewAccountID("account")
	tenant, _ := otogi.NewTenantID("tenant")
	conversation, _ := otogi.NewConversationID("conversation")
	kind, _ := otogi.NewEventKind("text.message")
	text, _ := otogi.NewText(value)
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	input, err := otogi.NewEventInput(otogi.EventInputParams{
		Platform: platform, Account: account, Tenant: tenant, Conversation: conversation,
		Kind: kind, SchemaVersion: 1, OccurredAt: now, IngestedAt: now, Text: text,
	})
	if err != nil {
		t.Fatalf("NewEventInput: %v", err)
	}
	return input
}

func inputTexts(inputs []otogi.EventInput) []string {
	texts := make([]string, len(inputs))
	for i := range inputs {
		texts[i] = inputs[i].Text().String()
	}
	return texts
}

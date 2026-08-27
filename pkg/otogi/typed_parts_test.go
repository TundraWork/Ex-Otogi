package otogi

import (
	"errors"
	"math"
	"strings"
	"testing"
)

func TestTypedPartPayloadsRoundTrip(t *testing.T) {
	id := mustTypedPartID(t, "typed")
	child := mustTypedPartID(t, "child")

	location, err := NewLocation(LocationParams{Latitude: 51.5074, Longitude: -0.1278, Label: "London"})
	if err != nil {
		t.Fatal(err)
	}
	locationPart, err := NewLocationPart(id, location, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	gotLocation, ok := locationPart.Location()
	if !ok || gotLocation.Latitude() != 51.5074 || gotLocation.Longitude() != -0.1278 || gotLocation.Label() != "London" {
		t.Fatalf("location = (%+v, %v)", gotLocation, ok)
	}

	contact, err := NewContact(ContactParams{DisplayName: "Ada Lovelace", Address: "ada@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	contactPart, err := NewContactPart(id, contact, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	gotContact, ok := contactPart.Contact()
	if !ok || gotContact.DisplayName() != "Ada Lovelace" || gotContact.Address() != "ada@example.test" {
		t.Fatalf("contact = (%+v, %v)", gotContact, ok)
	}

	poll, err := NewPoll(PollParams{Question: "Choose", Options: []string{"one", "two"}, Multiple: true})
	if err != nil {
		t.Fatal(err)
	}
	pollPart, err := NewPollPart(id, poll, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	gotPoll, ok := pollPart.Poll()
	if !ok || gotPoll.Question() != "Choose" || !gotPoll.Multiple() || len(gotPoll.Options()) != 2 {
		t.Fatalf("poll = (%+v, %v)", gotPoll, ok)
	}

	interactive, err := NewInteractive(InteractiveParams{Layout: "row", ActionID: "approve", Label: "Approve"})
	if err != nil {
		t.Fatal(err)
	}
	interactivePart, err := NewInteractivePart(id, interactive, []PartID{child}, nil)
	if err != nil {
		t.Fatal(err)
	}
	gotInteractive, ok := interactivePart.Interactive()
	if !ok || gotInteractive.Layout() != "row" || gotInteractive.ActionID() != "approve" || gotInteractive.Label() != "Approve" || len(interactivePart.Children()) != 1 {
		t.Fatalf("interactive = (%+v, %v), children = %v", gotInteractive, ok, interactivePart.Children())
	}

	embedded, err := NewEmbeddedMessage(EmbeddedMessageParams{Summary: "Forwarded", Reference: "message-42"})
	if err != nil {
		t.Fatal(err)
	}
	embeddedPart, err := NewEmbeddedMessagePart(id, embedded, []PartID{child}, nil)
	if err != nil {
		t.Fatal(err)
	}
	gotEmbedded, ok := embeddedPart.EmbeddedMessage()
	if !ok || gotEmbedded.Summary() != "Forwarded" || gotEmbedded.Reference() != "message-42" || len(embeddedPart.Children()) != 1 {
		t.Fatalf("embedded = (%+v, %v), children = %v", gotEmbedded, ok, embeddedPart.Children())
	}
	if _, ok := embeddedPart.Value(); ok {
		t.Fatal("known payload exposed as opaque extension")
	}
}

func TestTypedPayloadConstructorsValidateRequiredFields(t *testing.T) {
	tests := []struct {
		name string
		make func() error
	}{
		{"location non-finite", func() error { _, err := NewLocation(LocationParams{Latitude: math.NaN()}); return err }},
		{"location latitude", func() error { _, err := NewLocation(LocationParams{Latitude: 91}); return err }},
		{"contact display name", func() error { _, err := NewContact(ContactParams{Address: "ada@example.test"}); return err }},
		{"contact address", func() error { _, err := NewContact(ContactParams{DisplayName: "Ada"}); return err }},
		{"poll question", func() error { _, err := NewPoll(PollParams{Options: []string{"one", "two"}}); return err }},
		{"poll options", func() error { _, err := NewPoll(PollParams{Question: "Choose", Options: []string{"one"}}); return err }},
		{"interactive layout", func() error {
			_, err := NewInteractive(InteractiveParams{ActionID: "approve", Label: "Approve"})
			return err
		}},
		{"interactive action pair", func() error {
			_, err := NewInteractive(InteractiveParams{Layout: "row", ActionID: "approve"})
			return err
		}},
		{"embedded reference", func() error { _, err := NewEmbeddedMessage(EmbeddedMessageParams{Summary: "Forwarded"}); return err }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.make(); !errors.Is(err, ErrInvalid) {
				t.Fatalf("error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestTypedPayloadsDefensivelyCopyCollections(t *testing.T) {
	options := []string{"one", "two"}
	poll, err := NewPoll(PollParams{Question: "Choose", Options: options})
	if err != nil {
		t.Fatal(err)
	}
	options[0] = "changed"
	got := poll.Options()
	got[1] = "changed"
	if fresh := poll.Options(); fresh[0] != "one" || fresh[1] != "two" {
		t.Fatalf("options mutated: %v", fresh)
	}

	child := mustTypedPartID(t, "child")
	children := []PartID{child}
	interactive, err := NewInteractive(InteractiveParams{Layout: "column"})
	if err != nil {
		t.Fatal(err)
	}
	part, err := NewInteractivePart(mustTypedPartID(t, "root"), interactive, children, nil)
	if err != nil {
		t.Fatal(err)
	}
	children[0] = PartID{}
	returned := part.Children()
	returned[0] = PartID{}
	if part.Children()[0] != child {
		t.Fatal("part children were not defensively copied")
	}
}

func TestTypedPayloadBoundsAreChecked(t *testing.T) {
	tooLong := strings.Repeat("x", maxTypedPartStringBytes+1)
	if _, err := NewLocation(LocationParams{Label: tooLong}); !errors.Is(err, ErrCapacity) {
		t.Fatalf("location bound: %v", err)
	}
	if _, err := NewContact(ContactParams{DisplayName: tooLong, Address: "a"}); !errors.Is(err, ErrCapacity) {
		t.Fatalf("contact bound: %v", err)
	}
	if _, err := NewPoll(PollParams{Question: "Choose", Options: make([]string, maxPollOptions+1)}); !errors.Is(err, ErrCapacity) {
		t.Fatalf("poll option count bound: %v", err)
	}
	if _, err := NewInteractive(InteractiveParams{Layout: tooLong}); !errors.Is(err, ErrCapacity) {
		t.Fatalf("interactive bound: %v", err)
	}
	if _, err := NewEmbeddedMessage(EmbeddedMessageParams{Reference: tooLong}); !errors.Is(err, ErrCapacity) {
		t.Fatalf("embedded bound: %v", err)
	}
}

func TestTypedPartConstructorsRejectZeroPayloads(t *testing.T) {
	id := mustTypedPartID(t, "typed")
	tests := []struct {
		name string
		make func() error
	}{
		{"location", func() error { _, err := NewLocationPart(id, Location{}, nil, nil); return err }},
		{"contact", func() error { _, err := NewContactPart(id, Contact{}, nil, nil); return err }},
		{"poll", func() error { _, err := NewPollPart(id, Poll{}, nil, nil); return err }},
		{"interactive", func() error { _, err := NewInteractivePart(id, Interactive{}, nil, nil); return err }},
		{"embedded", func() error { _, err := NewEmbeddedMessagePart(id, EmbeddedMessage{}, nil, nil); return err }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.make(); !errors.Is(err, ErrInvalid) {
				t.Fatalf("error = %v, want ErrInvalid", err)
			}
		})
	}
}

func mustTypedPartID(t *testing.T, value string) PartID {
	t.Helper()
	id, err := NewPartID(value)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

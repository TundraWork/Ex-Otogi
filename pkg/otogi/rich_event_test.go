package otogi

import (
	"errors"
	"testing"
	"time"
)

const compileTimeCoreAction EventAction = ActionMessageCreated

func TestEventInputOwnsRichEnvelope(t *testing.T) {
	message := mustTextMessage(t, "body")
	extension := mustEnvelopeExtension(t, "https://example.test/envelope", []byte{1, 2, 3})
	locator, err := NewProviderLocator("https://example.test/provider", 1, []byte("source"))
	if err != nil {
		t.Fatal(err)
	}
	p := richEventInputParams(t, message)
	p.SourceEventID = mustValue(t, NewSourceEventID, "source")
	p.Actor = mustValue(t, NewActorID, "actor")
	p.Direction = DirectionIncoming
	p.EditedAt = p.OccurredAt.Add(time.Second)
	p.TraceID = mustValue(t, NewTraceID, "trace")
	p.CausalEventID = mustValue(t, NewCausalEventID, "cause")
	p.ProviderLocator = locator
	p.Extensions = []Extension{extension}

	input, err := NewEventInput(p)
	if err != nil {
		t.Fatal(err)
	}
	p.Extensions[0] = Extension{}
	gotExtensions := input.Extensions()
	gotExtensions[0] = Extension{}
	if input.Action() != ActionMessageCreated || input.Direction() != DirectionIncoming || input.Completeness() != CompletenessComplete {
		t.Fatal("rich routing values differ")
	}
	if edited, ok := input.EditedAt(); !ok || !edited.Equal(p.EditedAt) || edited.Location() != time.UTC {
		t.Fatalf("EditedAt = %v, %v", edited, ok)
	}
	if got, ok := input.ProviderLocator(); !ok || string(got.Data()) != "source" {
		t.Fatal("provider locator differs")
	}
	if len(input.Message().Parts()) != 1 || len(input.Extensions()) != 1 || input.Extensions()[0].NamespaceURI() != extension.NamespaceURI() {
		t.Fatal("owned rich content differs")
	}
}

func TestEventInputCompletenessControlsEmptyMessage(t *testing.T) {
	empty, err := NewMessage(nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, completeness := range []Completeness{CompletenessPartial, CompletenessRedacted, CompletenessUnavailable} {
		p := richEventInputParams(t, empty)
		p.Completeness = completeness
		locator, locatorErr := NewProviderLocator("https://example.test/provider", 1, []byte("known"))
		if locatorErr != nil {
			t.Fatal(locatorErr)
		}
		p.ProviderLocator = locator
		if _, err := NewEventInput(p); err != nil {
			t.Fatalf("%s empty event: %v", completeness, err)
		}
	}
	p := richEventInputParams(t, empty)
	if _, err := NewEventInput(p); !errors.Is(err, ErrInvalid) {
		t.Fatalf("complete empty event: %v", err)
	}
}

func TestEventActionValidation(t *testing.T) {
	for _, action := range []EventAction{compileTimeCoreAction, ActionMessageEdited, ActionMessageDeleted, ActionMessageService} {
		if _, err := NewEventAction(action.String()); err != nil {
			t.Fatalf("core action %q: %v", action, err)
		}
	}
	custom, err := NewEventAction("https://telegram.org/event/reaction.updated")
	if err != nil || custom.String() != "https://telegram.org/event/reaction.updated" {
		t.Fatalf("custom action: %q, %v", custom, err)
	}
	for _, value := range []string{"", "message.unknown", " reaction", "relative/path"} {
		if _, err := NewEventAction(value); !errors.Is(err, ErrInvalid) {
			t.Fatalf("action %q: %v", value, err)
		}
	}
}
func TestContractsRejectUnvalidatedDirectEventActions(t *testing.T) {
	invalid := EventAction("message.unknown")
	p := richEventInputParams(t, mustTextMessage(t, "body"))
	p.Action = invalid
	if _, err := NewEventInput(p); !errors.Is(err, ErrInvalid) {
		t.Fatalf("event input direct action: %v", err)
	}
	id := mustValue(t, NewModuleID, "module")
	if _, err := NewModuleDescriptor(id, []EventAction{invalid}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("module descriptor direct action: %v", err)
	}
}

func richEventInputParams(t *testing.T, message Message) EventInputParams {
	t.Helper()
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.FixedZone("local", 3600))
	return EventInputParams{
		Platform: mustValue(t, NewPlatformID, "platform"), Account: mustValue(t, NewAccountID, "account"),
		Tenant: mustValue(t, NewTenantID, "tenant"), Conversation: mustValue(t, NewConversationID, "conversation"),
		Action: ActionMessageCreated, Direction: DirectionIncoming, OccurredAt: now, IngestedAt: now.Add(time.Second),
		Completeness: CompletenessComplete, Message: message,
	}
}

func mustTextMessage(t *testing.T, value string) Message {
	t.Helper()
	id, err := NewPartID("text")
	if err != nil {
		t.Fatal(err)
	}
	part, err := NewTextPart(id, value, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	message, err := NewMessage([]PartID{id}, []Part{part}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return message
}

func mustEnvelopeExtension(t *testing.T, namespace string, data []byte) Extension {
	t.Helper()
	extension, err := NewExtension(namespace, "record", 1, "application/octet-stream", data)
	if err != nil {
		t.Fatal(err)
	}
	return extension
}

package kernel

import (
	"context"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

func TestRoutingProjectsCompleteRichEvent(t *testing.T) {
	platform, _ := otogi.NewPlatformID("platform")
	account, _ := otogi.NewAccountID("account")
	tenant, _ := otogi.NewTenantID("tenant")
	conversation, _ := otogi.NewConversationID("conversation")
	source, _ := otogi.NewSourceEventID("source")
	actor, _ := otogi.NewActorID("actor")
	trace, _ := otogi.NewTraceID("trace")
	causal, _ := otogi.NewCausalEventID("causal")
	locator, _ := otogi.NewProviderLocator("https://example.test/locator", 1, []byte("locator"))
	extension, _ := otogi.NewExtension("https://example.test/extension", "envelope", 1, "application/octet-stream", []byte("extension"))
	partID, _ := otogi.NewPartID("text")
	part, _ := otogi.NewTextPart(partID, "rich", nil, nil, nil)
	message, _ := otogi.NewMessage([]otogi.PartID{partID}, []otogi.Part{part}, nil, nil)
	occurred := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	edited := occurred.Add(time.Minute)
	ingested := edited.Add(time.Minute)
	input, err := otogi.NewEventInput(otogi.EventInputParams{
		Platform: platform, Account: account, Tenant: tenant, Conversation: conversation,
		SourceEventID: source, Actor: actor, Action: otogi.ActionMessageEdited, Direction: otogi.DirectionIncoming,
		OccurredAt: occurred, IngestedAt: ingested, EditedAt: edited, TraceID: trace, CausalEventID: causal,
		Completeness: otogi.CompletenessPartial, ProviderLocator: locator, Message: message, Extensions: []otogi.Extension{extension},
	})
	if err != nil {
		t.Fatal(err)
	}
	module := newTestModule(t, "rich", "edited", func(_ context.Context, event otogi.Event, _ otogi.Invocation) otogi.Result {
		gotSource, sourceOK := event.SourceEventID()
		gotActor, actorOK := event.Actor()
		gotEdited, editedOK := event.EditedAt()
		gotTrace, traceOK := event.TraceID()
		gotCausal, causalOK := event.CausalEventID()
		gotLocator, locatorOK := event.ProviderLocator()
		text, _, _ := event.Message().Parts()[0].Text()
		if event.Platform() != platform || event.Account() != account || event.Tenant() != tenant || event.Conversation() != conversation || !sourceOK || gotSource != source || !actorOK || gotActor != actor || event.Action() != otogi.ActionMessageEdited || event.Direction() != otogi.DirectionIncoming || !event.OccurredAt().Equal(occurred) || !event.IngestedAt().Equal(ingested) || !editedOK || !gotEdited.Equal(edited) || !traceOK || gotTrace != trace || !causalOK || gotCausal != causal || event.Completeness() != otogi.CompletenessPartial || !locatorOK || string(gotLocator.Data()) != "locator" || text != "rich" || len(event.Extensions()) != 1 || string(event.Extensions()[0].Data()) != "extension" {
			t.Fatal("rich event projection lost data")
		}
		return otogi.NewSucceededResult()
	})
	k := newTestKernel(t, DefaultConfig(), []otogi.Module{module}, &testMessenger{})
	if err := k.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := k.Submit(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if err := k.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
}

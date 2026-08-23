package llmchat

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"ex-otogi/pkg/otogi/ai"
	panel "ex-otogi/pkg/otogi/management"
)

func TestChatRequestLifecycleIsCausallyRooted(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		handlerErr   error
		wantTerminal string
	}{
		{name: "completed", wantTerminal: "chat.request.completed"},
		{name: "failed", handlerErr: errors.New("provider unavailable"), wantTerminal: "chat.request.failed"},
		{name: "canceled", handlerErr: context.Canceled, wantTerminal: "chat.request.canceled"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			recorder := &lifecycleRecorder{}
			now := time.Date(2026, time.August, 14, 12, 0, 0, 0, time.UTC)
			module := newTestModule(validModuleConfig())
			module.recorder = recorder
			module.clock = func() time.Time { return now }
			agent := module.cfg.Agents[0]
			event := testLLMChatEvent(agent.Name + " hello")

			ctx := panel.WithTrace(context.Background(), panel.TraceContext{TraceID: "trace-chat"})
			ctx = module.beginChatRequest(ctx, event, agent)
			module.finishChatRequest(ctx, event, agent, now.Add(-time.Second), test.handlerErr)

			if len(recorder.events) != 2 {
				t.Fatalf("event count = %d, want accepted plus one terminal event", len(recorder.events))
			}
			if recorder.events[0].Kind != "chat.request.accepted" || recorder.events[1].Kind != test.wantTerminal {
				t.Fatalf("event kinds = [%q,%q], want accepted/%s", recorder.events[0].Kind, recorder.events[1].Kind, test.wantTerminal)
			}
			if recorder.events[0].TraceID != "trace-chat" || recorder.events[1].TraceID != "trace-chat" {
				t.Fatalf("trace IDs = [%q,%q], want trace-chat", recorder.events[0].TraceID, recorder.events[1].TraceID)
			}
			if recorder.events[1].ParentEventID == nil || *recorder.events[1].ParentEventID != recorder.events[0].ID {
				t.Fatalf("terminal parent = %v, want accepted event %d", recorder.events[1].ParentEventID, recorder.events[0].ID)
			}
		})
	}
}

func TestChatRequestStateTransitionsAreObservable(t *testing.T) {
	recorder := &lifecycleRecorder{}
	module := newTestModule(validModuleConfig())
	module.recorder = recorder
	agent := module.cfg.Agents[0]
	event := testLLMChatEvent(agent.Name + " hello")
	ctx := panel.WithTrace(context.Background(), panel.TraceContext{TraceID: "trace-states"})
	ctx = module.beginChatRequest(ctx, event, agent)
	ctx = withChatLifecycle(ctx, &chatLifecycle{state: chatStateAccepted, m: module, agent: agent})

	for _, state := range []chatState{chatStateContextReady, chatStateGenerating, chatStateExecutingTools, chatStateGenerating, chatStateDelivering} {
		transitionChatState(ctx, state)
	}
	failure := ai.NewLLMFailure(ai.LLMFailureTool, false, 0, fmt.Errorf("tool failed"))
	module.finishChatRequest(ctx, event, agent, module.now(), failure)

	want := []string{
		"chat.request.accepted", "chat.request.state_changed", "chat.request.state_changed",
		"chat.request.state_changed", "chat.request.state_changed", "chat.request.state_changed",
		"chat.request.failed",
	}
	if len(recorder.events) != len(want) {
		t.Fatalf("event count = %d, want %d", len(recorder.events), len(want))
	}
	for index, kind := range want {
		if recorder.events[index].Kind != kind {
			t.Fatalf("event[%d] kind = %q, want %q", index, recorder.events[index].Kind, kind)
		}
	}
	payload, ok := recorder.events[len(recorder.events)-1].Payload.(panel.ChatRequestPayload)
	if !ok {
		t.Fatalf("terminal payload type = %T", recorder.events[len(recorder.events)-1].Payload)
	}
	if payload.State != string(chatStateFailed) || payload.FailureClass != string(ai.LLMFailureTool) || payload.MaxAttempts != llmRetryMaxAttempts {
		t.Fatalf("terminal payload = %#v, want failed/tool_failure/%d attempts", payload, llmRetryMaxAttempts)
	}
}

type lifecycleRecorder struct {
	events []panel.Event
}

func (r *lifecycleRecorder) RecordEvent(ctx context.Context, event panel.Event) (panel.Event, error) {
	event.ID = int64(len(r.events) + 1)
	if trace, ok := panel.TraceFromContext(ctx); ok {
		event.TraceID = trace.TraceID
		if event.ParentEventID == nil && trace.ParentEventID != nil {
			parentID := *trace.ParentEventID
			event.ParentEventID = &parentID
		}
	}
	r.events = append(r.events, event)
	return event, nil
}

func (*lifecycleRecorder) RecordArtifact(context.Context, panel.Artifact) (panel.Artifact, error) {
	return panel.Artifact{}, nil
}

func (*lifecycleRecorder) UpsertSnapshot(context.Context, panel.Snapshot) (panel.Snapshot, error) {
	return panel.Snapshot{}, nil
}

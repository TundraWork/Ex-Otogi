package llmchat

import (
	"context"
	"reflect"
	"time"

	panel "ex-otogi/pkg/otogi/management"
	"ex-otogi/pkg/otogi/platform"
)

func (m *Module) emitManagementEvent(
	ctx context.Context,
	event *platform.Event,
	kind string,
	summary string,
	payload any,
) {
	if m == nil || m.recorder == nil {
		return
	}
	managedEvent := panel.Event{
		OccurredAt:  m.now(),
		Category:    panel.EventCategoryMemory,
		Kind:        kind,
		Level:       panel.EventLevelDebug,
		Module:      m.Name(),
		Component:   "semantic-memory",
		Summary:     summary,
		PayloadType: llmchatPayloadTypeName(payload),
		Payload:     payload,
	}
	if stringsHasLLMPrefix(kind) {
		managedEvent.Category = panel.EventCategoryLLM
		managedEvent.Component = "tool-execution"
	}
	if event != nil {
		managedEvent.Platform = string(event.Source.Platform)
		managedEvent.ConversationID = event.Conversation.ID
		managedEvent.ActorID = event.Actor.ID
	}
	_, err := m.recorder.RecordEvent(ctx, managedEvent)
	if err != nil {
		return
	}
}

func (m *Module) recordToolExecuted(
	ctx context.Context,
	event *platform.Event,
	toolName string,
	success bool,
	elapsed time.Duration,
	err error,
) {
	payload := panel.LLMToolExecutedPayload{
		ToolName:  toolName,
		Success:   success,
		ElapsedMS: elapsed.Milliseconds(),
	}
	if err != nil {
		payload.Error = err.Error()
	}
	m.emitManagementEvent(ctx, event, "llm.tool.executed", "executed tool call", payload)
}

func llmchatPayloadTypeName(payload any) string {
	if payload == nil {
		return ""
	}
	typ := reflect.TypeOf(payload)
	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}

	return typ.Name()
}

func stringsHasLLMPrefix(kind string) bool {
	return len(kind) >= 4 && kind[:4] == "llm."
}

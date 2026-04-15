package llmchat

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	"ex-otogi/pkg/otogi/ai"
	panel "ex-otogi/pkg/otogi/management"
	"ex-otogi/pkg/otogi/platform"
)

func (m *Module) emitManagementEvent(
	ctx context.Context,
	event *platform.Event,
	kind string,
	summary string,
	description string,
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
		Subject:     summary,
		Description: description,
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
	toolCall ai.LLMToolCall,
	success bool,
	elapsed time.Duration,
	err error,
) {
	payload := panel.LLMToolExecutedPayload{
		ToolName:  toolCall.Name,
		Success:   success,
		ElapsedMS: elapsed.Milliseconds(),
	}
	if err != nil {
		payload.Error = err.Error()
	}
	successLabel := "success"
	if !success {
		successLabel = "failure"
	}
	m.emitManagementEvent(
		ctx,
		event,
		"llm.tool.executed",
		"executed tool call",
		toolExecutionDescription(toolCall, successLabel, elapsed, err),
		payload,
	)
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

func toolExecutionDescription(toolCall ai.LLMToolCall, successLabel string, elapsed time.Duration, err error) string {
	parts := make([]string, 0, 3)
	if strings.TrimSpace(toolCall.Name) != "" {
		parts = append(parts, fmt.Sprintf("%s %s in %dms", toolCall.Name, successLabel, elapsed.Milliseconds()))
	}
	if strings.TrimSpace(toolCall.Arguments) != "" {
		parts = append(parts, toolCall.Arguments)
	}
	if err != nil && strings.TrimSpace(err.Error()) != "" {
		parts = append(parts, err.Error())
	}

	return panel.TruncateDescription(strings.Join(parts, ": "))
}

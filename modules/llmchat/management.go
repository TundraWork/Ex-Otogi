package llmchat

import (
	"context"
	"fmt"
	"strings"
	"time"

	"ex-otogi/pkg/otogi/ai"
	panel "ex-otogi/pkg/otogi/management"
	"ex-otogi/pkg/otogi/platform"
)

func (m *Module) beginChatRequest(ctx context.Context, event *platform.Event, agent Agent) context.Context {
	if m == nil || m.recorder == nil {
		return ctx
	}
	recorded, err := m.recorder.RecordEvent(ctx, panel.Event{
		OccurredAt:     m.now(),
		Category:       panel.EventCategoryLLM,
		Kind:           "chat.request.accepted",
		Level:          panel.EventLevelInfo,
		Module:         m.Name(),
		Component:      "orchestrator",
		Platform:       string(event.Source.Platform),
		ConversationID: event.Conversation.ID,
		ActorID:        event.Actor.ID,
		Subject:        "accepted chat request",
		Description:    panel.TruncateDescription(fmt.Sprintf("%s using %s/%s", agent.Name, agent.Provider, agent.Model)),
		Payload: panel.ChatRequestPayload{
			Agent: agent.Name, Provider: agent.Provider, Model: agent.Model, Outcome: "accepted",
			State: string(chatStateAccepted), MaxAttempts: llmRetryMaxAttempts,
		},
	})
	if err != nil || recorded.ID == 0 {
		return ctx
	}

	trace, _ := panel.TraceFromContext(ctx)
	parentID := recorded.ID
	trace.ParentEventID = &parentID

	return panel.WithTrace(ctx, trace)
}

func (m *Module) finishChatRequest(
	ctx context.Context,
	event *platform.Event,
	agent Agent,
	startedAt time.Time,
	handlerErr error,
) {
	if m == nil || m.recorder == nil {
		return
	}

	outcome := "completed"
	kind := "chat.request.completed"
	level := panel.EventLevelInfo
	failureClass := ""
	terminalState := chatStateCompleted
	if handlerErr != nil {
		failureClass = string(ai.ClassifyLLMFailure(handlerErr))
		outcome, kind, level, terminalState = "failed", "chat.request.failed", panel.EventLevelError, chatStateFailed
		if ai.ClassifyLLMFailure(handlerErr) == ai.LLMFailureCanceled {
			outcome, kind, level, terminalState = "canceled", "chat.request.canceled", panel.EventLevelWarn, chatStateCanceled
		}
	}
	transitionChatState(ctx, terminalState)

	payload := panel.ChatRequestPayload{
		Agent: agent.Name, Provider: agent.Provider, Model: agent.Model, Outcome: outcome, State: string(terminalState),
		ElapsedMS: m.now().Sub(startedAt).Milliseconds(), FailureClass: failureClass,
		MaxAttempts: llmRetryMaxAttempts,
	}
	description := fmt.Sprintf("%s in %dms", outcome, payload.ElapsedMS)
	if handlerErr != nil {
		payload.Error = handlerErr.Error()
		description = handlerErr.Error()
	}
	if _, err := m.recorder.RecordEvent(ctx, panel.Event{
		OccurredAt:     m.now(),
		Category:       panel.EventCategoryLLM,
		Kind:           kind,
		Level:          level,
		Module:         m.Name(),
		Component:      "orchestrator",
		Platform:       string(event.Source.Platform),
		ConversationID: event.Conversation.ID,
		ActorID:        event.Actor.ID,
		Subject:        "chat request " + outcome,
		Description:    panel.TruncateDescription(description),
		Payload:        payload,
	}); err != nil && m.logger != nil {
		m.logger.ErrorContext(ctx, "record chat request terminal event", "error", err)
	}
}

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
	level := panel.EventLevelInfo
	if !success {
		level = panel.EventLevelWarn
	}
	managedEvent := panel.Event{
		Category: panel.EventCategoryTool, Kind: "tool.call.completed", Level: level,
		Module: m.Name(), Component: "tool-execution", Subject: "tool call " + successLabel,
		Description: toolExecutionDescription(toolCall, successLabel, elapsed, err),
		Payload:     payload,
	}
	if event != nil {
		managedEvent.Platform = string(event.Source.Platform)
		managedEvent.ConversationID = event.Conversation.ID
		managedEvent.ActorID = event.Actor.ID
	}
	if m != nil && m.recorder != nil {
		if _, recordErr := m.recorder.RecordEvent(ctx, managedEvent); recordErr != nil && m.logger != nil {
			m.logger.ErrorContext(ctx, "record tool call event", "error", recordErr)
		}
	}
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

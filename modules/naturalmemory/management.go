package naturalmemory

import (
	"context"
	"reflect"
	"time"

	"ex-otogi/pkg/otogi/ai"
	panel "ex-otogi/pkg/otogi/management"
)

func (m *Module) emitManagementEvent(
	ctx context.Context,
	scope *ai.LLMMemoryScope,
	kind string,
	summary string,
	description string,
	payload any,
) {
	m.recordManagementEvent(
		ctx,
		scope,
		panel.EventLevelDebug,
		"natural-memory",
		kind,
		summary,
		description,
		payload,
	)
}

func (m *Module) emitManagementWarningEvent(
	ctx context.Context,
	scope *ai.LLMMemoryScope,
	component string,
	kind string,
	summary string,
	description string,
	payload any,
) {
	m.recordManagementEvent(
		ctx,
		scope,
		panel.EventLevelWarn,
		component,
		kind,
		summary,
		description,
		payload,
	)
}

func (m *Module) emitManagementErrorEvent(
	ctx context.Context,
	scope *ai.LLMMemoryScope,
	component string,
	kind string,
	summary string,
	description string,
	payload any,
) {
	m.recordManagementEvent(
		ctx,
		scope,
		panel.EventLevelError,
		component,
		kind,
		summary,
		description,
		payload,
	)
}

func (m *Module) recordManagementEvent(
	ctx context.Context,
	scope *ai.LLMMemoryScope,
	level panel.EventLevel,
	component string,
	kind string,
	summary string,
	description string,
	payload any,
) {
	if m == nil || m.recorder == nil {
		return
	}
	event := newNaturalMemoryEvent(
		m.now(),
		scope,
		level,
		component,
		kind,
		summary,
		description,
		payload,
	)
	_, err := m.recorder.RecordEvent(ctx, event)
	if err != nil {
		return
	}
}

func newNaturalMemoryEvent(
	occurredAt time.Time,
	scope *ai.LLMMemoryScope,
	level panel.EventLevel,
	component string,
	kind string,
	summary string,
	description string,
	payload any,
) panel.Event {
	event := panel.Event{
		OccurredAt:  occurredAt.UTC(),
		Category:    panel.EventCategoryMemory,
		Kind:        kind,
		Level:       level,
		Module:      "naturalmemory",
		Component:   component,
		Subject:     summary,
		Description: description,
		PayloadType: naturalMemoryPayloadTypeName(payload),
		Payload:     payload,
	}
	if scope == nil {
		return event
	}

	event.TenantID = scope.TenantID
	event.Platform = scope.Platform
	event.ConversationID = scope.ConversationID

	return event
}

func naturalMemoryPayloadTypeName(payload any) string {
	if payload == nil {
		return ""
	}
	typ := reflect.TypeOf(payload)
	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}

	return typ.Name()
}

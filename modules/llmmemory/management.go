package llmmemory

import (
	"context"
	"reflect"

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
	if m == nil || m.recorder == nil {
		return
	}
	_, err := m.recorder.RecordEvent(ctx, panel.Event{
		Category:       panel.EventCategoryMemory,
		Kind:           kind,
		Level:          panel.EventLevelDebug,
		Module:         m.Name(),
		Component:      "semantic-store",
		TenantID:       memoryTenantID(scope),
		Platform:       memoryPlatform(scope),
		ConversationID: memoryConversationID(scope),
		Subject:        summary,
		Description:    description,
		PayloadType:    payloadTypeName(payload),
		Payload:        payload,
	})
	if err != nil {
		return
	}
}

func memoryTenantID(scope *ai.LLMMemoryScope) string {
	if scope == nil {
		return ""
	}

	return scope.TenantID
}

func memoryPlatform(scope *ai.LLMMemoryScope) string {
	if scope == nil {
		return ""
	}

	return scope.Platform
}

func memoryConversationID(scope *ai.LLMMemoryScope) string {
	if scope == nil {
		return ""
	}

	return scope.ConversationID
}

func payloadTypeName(payload any) string {
	if payload == nil {
		return ""
	}
	typ := reflect.TypeOf(payload)
	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}

	return typ.Name()
}

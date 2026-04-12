package naturalmemory

import (
	"context"
	"reflect"

	panel "ex-otogi/pkg/otogi/management"
)

func (m *Module) emitManagementEvent(ctx context.Context, kind string, summary string, description string, payload any) {
	if m == nil || m.recorder == nil {
		return
	}
	_, err := m.recorder.RecordEvent(ctx, panel.Event{
		OccurredAt:  m.now(),
		Category:    panel.EventCategoryMemory,
		Kind:        kind,
		Level:       panel.EventLevelDebug,
		Module:      m.Name(),
		Component:   "natural-memory",
		Subject:     summary,
		Description: description,
		PayloadType: naturalMemoryPayloadTypeName(payload),
		Payload:     payload,
	})
	if err != nil {
		return
	}
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

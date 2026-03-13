package llmchat

import (
	"context"
	"reflect"

	"ex-otogi/pkg/otogi/platform"
)

type editPayload struct {
	Text     string
	Entities []platform.TextEntity
}

func plainEditPayload(text string) editPayload {
	return editPayload{
		Text: text,
	}
}

func (p editPayload) Equal(other editPayload) bool {
	return p.Text == other.Text && reflect.DeepEqual(p.Entities, other.Entities)
}

func (m *Module) parseEditPayload(ctx context.Context, text string) (editPayload, error) {
	if m == nil || m.parser == nil {
		return plainEditPayload(text), nil
	}

	parsed, err := m.parser.ParseMarkdown(ctx, text)
	if err != nil {
		return plainEditPayload(text), err
	}

	return editPayload{
		Text:     parsed.Text,
		Entities: append([]platform.TextEntity(nil), parsed.Entities...),
	}, nil
}

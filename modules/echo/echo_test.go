package echo

import (
	"context"
	"testing"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

type invocation struct{ sent []otogi.Message }

func (i *invocation) Send(_ context.Context, _ string, msg otogi.Message) (otogi.EventRef, error) {
	i.sent = append(i.sent, msg)
	return otogi.EventRef{}, nil
}

func TestHandle(t *testing.T) {
	tests := []struct {
		name      string
		message   otogi.Message
		direction otogi.Direction
		want      []string
	}{
		{"text", otogi.TextMessage("hello"), otogi.DirectionIncoming, []string{"echo: hello"}},
		{"no text", otogi.Message{Roots: []otogi.PartID{"m"}, Parts: []otogi.Part{{ID: "m", Kind: otogi.PartMedia, Media: &otogi.Media{}}}}, otogi.DirectionIncoming, nil},
		{"own message", otogi.TextMessage("echo: hello"), otogi.DirectionOutgoing, nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			inv := &invocation{}
			if err := New().Handle(context.Background(), otogi.Event{Message: test.message, Direction: test.direction}, inv); err != nil {
				t.Fatal(err)
			}
			if len(inv.sent) != len(test.want) {
				t.Fatalf("sent %d messages, want %d", len(inv.sent), len(test.want))
			}
			for i, want := range test.want {
				if got := inv.sent[i].PlainText(); got != want {
					t.Fatalf("sent %q, want %q", got, want)
				}
			}
		})
	}
}

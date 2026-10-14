package otogi

import (
	"errors"
	"testing"
)

func text(id PartID, s string, children ...PartID) Part {
	return Part{ID: id, Kind: PartText, Text: s, Children: children}
}

func TestMessageValidate(t *testing.T) {
	tests := []struct {
		name    string
		message Message
		valid   bool
	}{
		{"empty", Message{}, true},
		{"single text", TextMessage("hi"), true},
		{"shared child DAG", Message{Roots: []PartID{"a", "b"}, Parts: []Part{text("a", "a", "c"), text("b", "b", "c"), text("c", "c")}}, true},
		{"annotation on rune boundaries", Message{Roots: []PartID{"t"}, Parts: []Part{{ID: "t", Kind: PartText, Text: "héllo", Annotations: []Annotation{{Offset: 0, Length: 3, Kind: AnnotationBold}}}}}, true},
		{"roots without parts", Message{Roots: []PartID{"a"}}, false},
		{"parts without roots", Message{Parts: []Part{text("a", "a")}}, false},
		{"duplicate part", Message{Roots: []PartID{"a"}, Parts: []Part{text("a", "a"), text("a", "b")}}, false},
		{"missing root", Message{Roots: []PartID{"x"}, Parts: []Part{text("a", "a")}}, false},
		{"duplicate root", Message{Roots: []PartID{"a", "a"}, Parts: []Part{text("a", "a")}}, false},
		{"missing child", Message{Roots: []PartID{"a"}, Parts: []Part{text("a", "a", "x")}}, false},
		{"cycle", Message{Roots: []PartID{"a"}, Parts: []Part{text("a", "a", "b"), text("b", "b", "a")}}, false},
		{"unreachable part", Message{Roots: []PartID{"a"}, Parts: []Part{text("a", "a"), text("b", "b")}}, false},
		{"unknown kind", Message{Roots: []PartID{"a"}, Parts: []Part{{ID: "a", Kind: "hologram"}}}, false},
		{"media without payload", Message{Roots: []PartID{"a"}, Parts: []Part{{ID: "a", Kind: PartMedia}}}, false},
		{"empty text", Message{Roots: []PartID{"a"}, Parts: []Part{{ID: "a", Kind: PartText}}}, false},
		{"annotation past end", Message{Roots: []PartID{"t"}, Parts: []Part{{ID: "t", Kind: PartText, Text: "hi", Annotations: []Annotation{{Offset: 1, Length: 5}}}}}, false},
		{"annotation inside rune", Message{Roots: []PartID{"t"}, Parts: []Part{{ID: "t", Kind: PartText, Text: "héllo", Annotations: []Annotation{{Offset: 0, Length: 2}}}}}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.message.Validate()
			if test.valid && err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
			if !test.valid && !errors.Is(err, ErrInvalid) {
				t.Fatalf("Validate() = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestEventValidate(t *testing.T) {
	valid := Event{Platform: "p", Account: "a", Conversation: Conversation{ID: "c"}, Action: ActionMessageCreated, Message: TextMessage("hi")}
	tests := []struct {
		name  string
		edit  func(*Event)
		valid bool
	}{
		{"valid", func(*Event) {}, true},
		{"custom URI action", func(e *Event) { e.Action = "https://example.org/actions/reaction" }, true},
		{"missing platform", func(e *Event) { e.Platform = "" }, false},
		{"missing conversation", func(e *Event) { e.Conversation.ID = "" }, false},
		{"relative custom action", func(e *Event) { e.Action = "reaction" }, false},
		{"invalid message", func(e *Event) { e.Message.Roots = []PartID{"missing"} }, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			event := valid
			test.edit(&event)
			err := event.Validate()
			if test.valid != (err == nil) || (!test.valid && !errors.Is(err, ErrInvalid)) {
				t.Fatalf("Validate() = %v, want valid=%v", err, test.valid)
			}
		})
	}
}

func TestPlainText(t *testing.T) {
	m := Message{Roots: []PartID{"a", "m", "b"}, Parts: []Part{text("a", "foo "), {ID: "m", Kind: PartMedia, Media: &Media{}}, text("b", "bar")}}
	if got := m.PlainText(); got != "foo bar" {
		t.Fatalf("PlainText() = %q", got)
	}
}

func TestIdempotencyKeyFor(t *testing.T) {
	key := IdempotencyKeyFor("e1", "m1", "reply")
	if key != IdempotencyKeyFor("e1", "m1", "reply") {
		t.Fatal("key is not deterministic")
	}
	// Length-prefixing keeps differently split inputs apart.
	for _, other := range []IdempotencyKey{IdempotencyKeyFor("e1", "m1", "reply2"), IdempotencyKeyFor("e1m", "1", "reply"), IdempotencyKeyFor("e2", "m1", "reply")} {
		if other == key {
			t.Fatal("distinct inputs share a key")
		}
	}
}

package otogi

import "strings"

// RichText builds annotated text piece by piece, for modules composing
// formatted messages. The zero value is empty and ready to use.
type RichText struct {
	text        strings.Builder
	annotations []Annotation
}

// Write appends plain text.
func (r *RichText) Write(s string) { r.text.WriteString(s) }

// Mark appends whatever write writes and annotates it with kind and target.
// Nothing is annotated if write writes nothing.
func (r *RichText) Mark(kind AnnotationKind, target string, write func()) {
	start := r.text.Len()
	write()
	if r.text.Len() > start {
		r.annotations = append(r.annotations, Annotation{Offset: start, Length: r.text.Len() - start, Kind: kind, Target: target})
	}
}

// Append appends text with annotations whose offsets are relative to text.
func (r *RichText) Append(text string, annotations []Annotation) {
	base := r.text.Len()
	for _, a := range annotations {
		a.Offset += base
		r.annotations = append(r.annotations, a)
	}
	r.text.WriteString(text)
}

// Len is the length of the text in bytes.
func (r *RichText) Len() int { return r.text.Len() }

// String returns the text.
func (r *RichText) String() string { return r.text.String() }

// Annotations returns the annotations, in the order they were closed.
func (r *RichText) Annotations() []Annotation { return r.annotations }

// Message returns a message with the text as its only part.
func (r *RichText) Message() Message {
	m := TextMessage(r.String())
	m.Parts[0].Annotations = r.annotations
	return m
}

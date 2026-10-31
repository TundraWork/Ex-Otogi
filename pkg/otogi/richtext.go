package otogi

import (
	"slices"
	"strings"
	"unicode/utf8"
)

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

// Slice returns r[from:to], byte offsets, with annotations clipped to the
// range.
func (r *RichText) Slice(from, to int) *RichText {
	var kept []Annotation
	for _, a := range r.annotations {
		start, end := max(a.Offset, from), min(a.Offset+a.Length, to)
		if start < end {
			a.Offset, a.Length = start-from, end-start
			kept = append(kept, a)
		}
	}
	out := &RichText{}
	out.Append(r.String()[from:to], kept)
	return out
}

// Split cuts r into the longest first part that fits and the rest, for
// platforms that limit message size. fits reports whether a prefix of the
// text, carrying the given number of annotations, fits. Split cuts at the
// last line end in the second half of what fits, else at the last point
// outside all formatting but quotes, else where it must. Formatting across
// the cut continues on both sides, so nothing is lost. The line ends at the
// cut are dropped. first is empty if not even one character fits.
func (r *RichText) Split(fits func(prefix string, annotations int) bool) (first, rest *RichText) {
	text := r.String()
	started := func(at int) int {
		n := 0
		for _, a := range r.annotations {
			if a.Offset < at {
				n++
			}
		}
		return n
	}
	end := 0
	for i, c := range text {
		next := i + utf8.RuneLen(c)
		if !fits(text[:next], started(next)) {
			break
		}
		end = next
	}
	if end == len(text) {
		return r.Slice(0, len(text)), &RichText{}
	}
	cut := end
	if j := strings.LastIndexByte(text[:end], '\n'); j >= 0 && j+1 >= end/2 {
		cut = j + 1
	} else {
		for p := end; p > 0; {
			inside := slices.ContainsFunc(r.annotations, func(a Annotation) bool {
				return a.Kind != AnnotationBlockQuote && a.Kind != AnnotationExpandableBlockQuote && a.Offset < p && p < a.Offset+a.Length
			})
			if !inside {
				cut = p
				break
			}
			_, size := utf8.DecodeLastRuneInString(text[:p])
			p -= size
		}
	}
	return r.Slice(0, len(strings.TrimRight(text[:cut], "\n"))), r.Slice(cut+len(text[cut:])-len(strings.TrimLeft(text[cut:], "\n")), len(text))
}

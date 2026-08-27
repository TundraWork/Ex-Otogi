package otogi

import (
	"fmt"
	"unicode/utf8"
)

// PartID identifies one part within a message.
type PartID struct{ value string }

// NewPartID validates a stable part identifier.
func NewPartID(value string) (PartID, error) {
	if err := validateIdentifier(value); err != nil {
		return PartID{}, err
	}
	return PartID{value: value}, nil
}

// String returns the exact part identifier.
func (v PartID) String() string { return v.value }

// PartKind identifies a part's payload variant.
type PartKind string

const (
	// PartText holds UTF-8 text and annotations.
	PartText PartKind = "text"
	// PartMedia holds media metadata.
	PartMedia PartKind = "media"
	// PartFile holds file metadata.
	PartFile PartKind = "file"
	// PartLocation holds a typed location codec value.
	PartLocation PartKind = "location"
	// PartContact holds a typed contact codec value.
	PartContact PartKind = "contact"
	// PartPoll holds a typed poll codec value.
	PartPoll PartKind = "poll"
	// PartInteractive holds a typed interactive codec value.
	PartInteractive PartKind = "interactive"
	// PartEmbeddedMessage holds an embedded-message snapshot descriptor.
	PartEmbeddedMessage PartKind = "embedded_message"
	// PartOpaque holds unsupported protocol content losslessly.
	PartOpaque PartKind = "opaque"
)

// Part is an immutable discriminated rich-content value.
type Part struct {
	id          PartID
	kind        PartKind
	children    []PartID
	extensions  []Extension
	text        string
	annotations []Annotation
	media       Media
	value       Extension
}

// NewTextPart constructs a UTF-8 text part and validates annotation byte ranges.
func NewTextPart(id PartID, text string, annotations []Annotation, children []PartID, extensions []Extension) (Part, error) {
	if len(text) > 64*1024 || len(annotations) > 128 || len(children) > 64 || len(extensions) > 64 {
		return Part{}, fmt.Errorf("text part limits: %w", ErrCapacity)
	}
	if id.value == "" || !utf8.ValidString(text) || text == "" && len(annotations) == 0 {
		return Part{}, fmt.Errorf("text part: %w", ErrInvalid)
	}
	for _, annotation := range annotations {
		end, ok := checkedAddInt(annotation.offset, annotation.length)
		if !ok || annotation.id == "" || end > len(text) || !utf8Boundary(text, annotation.offset) || !utf8Boundary(text, end) {
			return Part{}, fmt.Errorf("text annotation range: %w", ErrInvalid)
		}
	}
	return Part{id: id, kind: PartText, text: text, annotations: cloneAnnotations(annotations), children: clonePartIDs(children), extensions: cloneExtensions(extensions)}, nil
}

// NewMediaPart constructs a media or file part.
func NewMediaPart(id PartID, kind PartKind, media Media, children []PartID, extensions []Extension) (Part, error) {
	if len(children) > 64 || len(extensions) > 64 {
		return Part{}, fmt.Errorf("media part limits: %w", ErrCapacity)
	}
	if id.value == "" || (kind != PartMedia && kind != PartFile) || media.source.kind == "" {
		return Part{}, fmt.Errorf("media part: %w", ErrInvalid)
	}
	return Part{id: id, kind: kind, media: media, children: clonePartIDs(children), extensions: cloneExtensions(extensions)}, nil
}

// NewTypedPart constructs a known non-text, non-media part using a namespaced typed codec value.
func NewTypedPart(id PartID, kind PartKind, value Extension, children []PartID, extensions []Extension) (Part, error) {
	if len(children) > 64 || len(extensions) > 64 {
		return Part{}, fmt.Errorf("typed part limits: %w", ErrCapacity)
	}
	if id.value == "" || !typedPartKind(kind) || value.namespaceURI == "" {
		return Part{}, fmt.Errorf("typed part: %w", ErrInvalid)
	}
	return Part{id: id, kind: kind, value: value, children: clonePartIDs(children), extensions: cloneExtensions(extensions)}, nil
}

// NewOpaquePart constructs a lossless unsupported protocol part.
func NewOpaquePart(id PartID, value Extension, children []PartID, extensions []Extension) (Part, error) {
	if len(children) > 64 || len(extensions) > 64 {
		return Part{}, fmt.Errorf("opaque part limits: %w", ErrCapacity)
	}
	if id.value == "" || value.namespaceURI == "" {
		return Part{}, fmt.Errorf("opaque part: %w", ErrInvalid)
	}
	return Part{id: id, kind: PartOpaque, value: value, children: clonePartIDs(children), extensions: cloneExtensions(extensions)}, nil
}

// ID returns the part's stable message-local identifier.
func (v Part) ID() PartID { return v.id }

// Kind returns the payload variant.
func (v Part) Kind() PartKind { return v.kind }

// Children returns ordered child part identifiers.
func (v Part) Children() []PartID { return clonePartIDs(v.children) }

// Extensions returns ordered part extensions.
func (v Part) Extensions() []Extension { return cloneExtensions(v.extensions) }

// Text returns copied text annotations and whether this is a text part.
func (v Part) Text() (string, []Annotation, bool) {
	return v.text, cloneAnnotations(v.annotations), v.kind == PartText
}

// Media returns media metadata and whether this is a media or file part.
func (v Part) Media() (Media, bool) { return v.media, v.kind == PartMedia || v.kind == PartFile }

// Value returns the typed or opaque codec value when present.
func (v Part) Value() (Extension, bool) {
	return v.value, typedPartKind(v.kind) || v.kind == PartOpaque
}

func typedPartKind(kind PartKind) bool {
	switch kind {
	case PartLocation, PartContact, PartPoll, PartInteractive, PartEmbeddedMessage:
		return true
	default:
		return false
	}
}

func clonePartIDs(value []PartID) []PartID {
	out := make([]PartID, len(value))
	copy(out, value)
	return out
}
func cloneAnnotations(value []Annotation) []Annotation {
	out := make([]Annotation, len(value))
	copy(out, value)
	return out
}
func utf8Boundary(text string, offset int) bool {
	return offset == 0 || offset == len(text) || offset > 0 && offset < len(text) && utf8.RuneStart(text[offset])
}
func checkedAddInt(a, b int) (int, bool) {
	if b > int(^uint(0)>>1)-a {
		return 0, false
	}
	return a + b, true
}

package otogi

import (
	"fmt"
	"unicode/utf8"
)

// AnnotationKind identifies portable text annotation semantics.
type AnnotationKind string

const (
	// AnnotationBold marks bold text.
	AnnotationBold AnnotationKind = "bold"
	// AnnotationItalic marks italic text.
	AnnotationItalic AnnotationKind = "italic"
	// AnnotationUnderline marks underlined text.
	AnnotationUnderline AnnotationKind = "underline"
	// AnnotationStrike marks struck text.
	AnnotationStrike AnnotationKind = "strike"
	// AnnotationSpoiler marks concealed text.
	AnnotationSpoiler AnnotationKind = "spoiler"
	// AnnotationCode marks inline code.
	AnnotationCode AnnotationKind = "code"
	// AnnotationPreformatted marks preformatted text.
	AnnotationPreformatted AnnotationKind = "preformatted"
	// AnnotationBlockQuote marks quoted text.
	AnnotationBlockQuote AnnotationKind = "block_quote"
	// AnnotationLink marks a link.
	AnnotationLink AnnotationKind = "link"
	// AnnotationMention marks a mention.
	AnnotationMention AnnotationKind = "mention"
	// AnnotationHashtag marks a hashtag.
	AnnotationHashtag AnnotationKind = "hashtag"
	// AnnotationCommand marks a command.
	AnnotationCommand AnnotationKind = "command"
	// AnnotationEmail marks an email address.
	AnnotationEmail AnnotationKind = "email"
	// AnnotationEmoji marks an emoji.
	AnnotationEmoji AnnotationKind = "emoji"
	// AnnotationTimestamp marks a timestamp.
	AnnotationTimestamp AnnotationKind = "timestamp"
)

// Annotation is an immutable byte range over a text part.
type Annotation struct {
	id         string
	offset     int
	length     int
	kind       AnnotationKind
	target     string
	extensions []Extension
}

// NewAnnotation validates annotation-local fields and owns extensions.
func NewAnnotation(id string, offset, length int, kind AnnotationKind, target string, extensions []Extension) (Annotation, error) {
	if len(extensions) > 64 {
		return Annotation{}, fmt.Errorf("annotation extensions: %w", ErrCapacity)
	}
	if !validRichString(id) || offset < 0 || length < 0 || !validAnnotationKind(kind) || !utf8.ValidString(target) {
		return Annotation{}, fmt.Errorf("annotation: %w", ErrInvalid)
	}
	return Annotation{id: id, offset: offset, length: length, kind: kind, target: target, extensions: cloneExtensions(extensions)}, nil
}

// ID returns the stable annotation identifier.
func (v Annotation) ID() string { return v.id }

// Offset returns the byte offset in the containing UTF-8 text.
func (v Annotation) Offset() int { return v.offset }

// Length returns the byte length in the containing UTF-8 text.
func (v Annotation) Length() int { return v.length }

// Kind returns the annotation semantics.
func (v Annotation) Kind() AnnotationKind { return v.kind }

// Target returns the optional annotation target or reference.
func (v Annotation) Target() string { return v.target }

// Extensions returns an ordered defensive copy of extension values.
func (v Annotation) Extensions() []Extension { return cloneExtensions(v.extensions) }

func validAnnotationKind(kind AnnotationKind) bool {
	switch kind {
	case AnnotationBold, AnnotationItalic, AnnotationUnderline, AnnotationStrike, AnnotationSpoiler, AnnotationCode, AnnotationPreformatted, AnnotationBlockQuote, AnnotationLink, AnnotationMention, AnnotationHashtag, AnnotationCommand, AnnotationEmail, AnnotationEmoji, AnnotationTimestamp:
		return true
	default:
		return false
	}
}

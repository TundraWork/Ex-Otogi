package otogi

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Message is rich content as an ordered graph of parts, plus relations to
// other events. Roots list the top-level parts in reading order; a part's
// Children nest under it. The graph is acyclic and every part is reachable
// from a root. A message with no parts is valid, for example on a deletion.
type Message struct {
	// Roots are the top-level parts in order.
	Roots []PartID `json:"roots,omitempty"`
	// Parts holds every part, in original order.
	Parts []Part `json:"parts,omitempty"`
	// Relations link this message to other events (reply, edit target, ...).
	Relations []Relation `json:"relations,omitempty"`
	// Extensions carry platform message data with no portable meaning yet.
	Extensions []Extension `json:"extensions,omitempty"`
}

// PartKind says which payload field of a Part is set.
type PartKind string

const (
	// PartText carries Text and Annotations.
	PartText PartKind = "text"
	// PartMedia carries Media for an image, video, audio, sticker, and so on.
	PartMedia PartKind = "media"
	// PartFile carries Media for a generic file.
	PartFile PartKind = "file"
	// PartLocation carries Location.
	PartLocation PartKind = "location"
	// PartContact carries Contact.
	PartContact PartKind = "contact"
	// PartPoll carries Poll.
	PartPoll PartKind = "poll"
	// PartInteractive carries Interactive, such as a button or row.
	PartInteractive PartKind = "interactive"
	// PartEmbeddedMessage carries Embedded, such as a quoted or forwarded message.
	PartEmbeddedMessage PartKind = "embedded_message"
	// PartOpaque carries Opaque platform content with no portable meaning.
	PartOpaque PartKind = "opaque"
)

// Part is one piece of content. Kind selects which payload field is set.
type Part struct {
	// ID identifies the part within its message.
	ID PartID `json:"id"`
	// Kind selects the payload.
	Kind PartKind `json:"kind"`
	// Children are nested parts in order.
	Children []PartID `json:"children,omitempty"`
	// Text is the UTF-8 text of a text part.
	Text string `json:"text,omitempty"`
	// Annotations are formatting and entity ranges over Text.
	Annotations []Annotation `json:"annotations,omitempty"`
	// Media describes a media or file part.
	Media *Media `json:"media,omitempty"`
	// Location describes a location part.
	Location *Location `json:"location,omitempty"`
	// Contact describes a contact part.
	Contact *Contact `json:"contact,omitempty"`
	// Poll describes a poll part.
	Poll *Poll `json:"poll,omitempty"`
	// Interactive describes an interactive part.
	Interactive *Interactive `json:"interactive,omitempty"`
	// Embedded describes an embedded message part.
	Embedded *EmbeddedMessage `json:"embedded,omitempty"`
	// Opaque holds the platform content of an opaque part.
	Opaque *Extension `json:"opaque,omitempty"`
	// Extensions carry platform part data with no portable meaning yet.
	Extensions []Extension `json:"extensions,omitempty"`
}

// TextMessage returns a message with a single text part.
func TextMessage(text string) Message {
	return Message{Roots: []PartID{"text"}, Parts: []Part{{ID: "text", Kind: PartText, Text: text}}}
}

// PlainText concatenates the text of all text parts in part order.
func (m Message) PlainText() string {
	var b strings.Builder
	for _, part := range m.Parts {
		if part.Kind == PartText {
			b.WriteString(part.Text)
		}
	}
	return b.String()
}

// Validate reports an error wrapping ErrInvalid when the part graph is
// malformed or a part lacks the payload its Kind requires.
func (m Message) Validate() error {
	if len(m.Parts) == 0 {
		if len(m.Roots) != 0 {
			return fmt.Errorf("message: roots without parts: %w", ErrInvalid)
		}
		return nil
	}
	index := make(map[PartID]int, len(m.Parts))
	for i, part := range m.Parts {
		if _, dup := index[part.ID]; dup || part.ID == "" {
			return fmt.Errorf("message: empty or duplicate part ID %q: %w", part.ID, ErrInvalid)
		}
		index[part.ID] = i
		if err := part.validate(); err != nil {
			return err
		}
	}
	if len(m.Roots) == 0 {
		return fmt.Errorf("message: parts without roots: %w", ErrInvalid)
	}
	const (
		unvisited = iota
		visiting
		done
	)
	state := make([]int, len(m.Parts))
	var visit func(PartID) error
	visit = func(id PartID) error {
		i, ok := index[id]
		if !ok {
			return fmt.Errorf("message: reference to missing part %q: %w", id, ErrInvalid)
		}
		switch state[i] {
		case visiting:
			return fmt.Errorf("message: cycle at part %q: %w", id, ErrInvalid)
		case done:
			return nil
		}
		state[i] = visiting
		for _, child := range m.Parts[i].Children {
			if err := visit(child); err != nil {
				return err
			}
		}
		state[i] = done
		return nil
	}
	seenRoot := make(map[PartID]bool, len(m.Roots))
	for _, root := range m.Roots {
		if seenRoot[root] {
			return fmt.Errorf("message: duplicate root %q: %w", root, ErrInvalid)
		}
		seenRoot[root] = true
		if err := visit(root); err != nil {
			return err
		}
	}
	for i, s := range state {
		if s != done {
			return fmt.Errorf("message: part %q unreachable from roots: %w", m.Parts[i].ID, ErrInvalid)
		}
	}
	return nil
}

func (p Part) validate() error {
	var ok bool
	switch p.Kind {
	case PartText:
		ok = p.Text != "" || len(p.Annotations) > 0
		for _, a := range p.Annotations {
			end := a.Offset + a.Length
			if a.Offset < 0 || a.Length < 0 || end > len(p.Text) || !utf8Boundary(p.Text, a.Offset) || !utf8Boundary(p.Text, end) {
				return fmt.Errorf("part %q: annotation [%d,+%d) outside text or not on a UTF-8 boundary: %w", p.ID, a.Offset, a.Length, ErrInvalid)
			}
		}
	case PartMedia, PartFile:
		ok = p.Media != nil
	case PartLocation:
		ok = p.Location != nil
	case PartContact:
		ok = p.Contact != nil
	case PartPoll:
		ok = p.Poll != nil
	case PartInteractive:
		ok = p.Interactive != nil
	case PartEmbeddedMessage:
		ok = p.Embedded != nil
	case PartOpaque:
		ok = p.Opaque != nil
	default:
		return fmt.Errorf("part %q: unknown kind %q: %w", p.ID, p.Kind, ErrInvalid)
	}
	if !ok {
		return fmt.Errorf("part %q: missing %s payload: %w", p.ID, p.Kind, ErrInvalid)
	}
	return nil
}

func utf8Boundary(s string, i int) bool { return i == len(s) || utf8.RuneStart(s[i]) }

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
	// PartLocation holds protocol-neutral geographic coordinates.
	PartLocation PartKind = "location"
	// PartContact holds a protocol-neutral contact identity.
	PartContact PartKind = "contact"
	// PartPoll holds a protocol-neutral poll.
	PartPoll PartKind = "poll"
	// PartInteractive holds a protocol-neutral layout or action descriptor.
	PartInteractive PartKind = "interactive"
	// PartEmbeddedMessage holds embedded-message reference metadata.
	PartEmbeddedMessage PartKind = "embedded_message"
	// PartOpaque holds unsupported protocol content losslessly.
	PartOpaque PartKind = "opaque"
)

// Part is an immutable discriminated rich-content value.
type Part struct {
	id              PartID
	kind            PartKind
	children        []PartID
	extensions      []Extension
	text            string
	annotations     []Annotation
	media           Media
	location        Location
	contact         Contact
	poll            Poll
	interactive     Interactive
	embeddedMessage EmbeddedMessage
	value           Extension
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

// LocationParams supplies protocol-neutral geographic location fields.
type LocationParams struct {
	// Latitude is the latitude in degrees from -90 through 90.
	Latitude float64
	// Longitude is the longitude in degrees from -180 through 180.
	Longitude float64
	// Label is an optional human-readable place label.
	Label string
}

// Location is an immutable protocol-neutral geographic location.
type Location struct {
	latitude  float64
	longitude float64
	label     string
	valid     bool
}

// NewLocation validates geographic coordinates and an optional label.
func NewLocation(p LocationParams) (Location, error) {
	if typedStringTooLong(p.Label) {
		return Location{}, fmt.Errorf("location: %w", ErrCapacity)
	}
	if !finite(p.Latitude) || !finite(p.Longitude) || p.Latitude < -90 || p.Latitude > 90 || p.Longitude < -180 || p.Longitude > 180 || !utf8.ValidString(p.Label) {
		return Location{}, fmt.Errorf("location: %w", ErrInvalid)
	}
	return Location{latitude: p.Latitude, longitude: p.Longitude, label: p.Label, valid: true}, nil
}

// Latitude returns the latitude in degrees.
func (v Location) Latitude() float64 { return v.latitude }

// Longitude returns the longitude in degrees.
func (v Location) Longitude() float64 { return v.longitude }

// Label returns the optional human-readable place label.
func (v Location) Label() string { return v.label }

// ContactParams supplies protocol-neutral contact identity fields.
type ContactParams struct {
	// DisplayName is the contact's human-readable name.
	DisplayName string
	// Address is a stable address such as a phone number, email address, or user URI.
	Address string
}

// Contact is an immutable protocol-neutral contact identity.
type Contact struct {
	displayName string
	address     string
}

// NewContact validates a contact identity.
func NewContact(p ContactParams) (Contact, error) {
	if typedStringTooLong(p.DisplayName) || typedStringTooLong(p.Address) {
		return Contact{}, fmt.Errorf("contact: %w", ErrCapacity)
	}
	if !validRichString(p.DisplayName) || !validRichString(p.Address) {
		return Contact{}, fmt.Errorf("contact: %w", ErrInvalid)
	}
	return Contact{displayName: p.DisplayName, address: p.Address}, nil
}

// DisplayName returns the contact's human-readable name.
func (v Contact) DisplayName() string { return v.displayName }

// Address returns the contact's stable address.
func (v Contact) Address() string { return v.address }

const (
	maxTypedPartStringBytes = 4 * 1024
	maxPollOptions          = 64
)

// PollParams supplies protocol-neutral poll fields.
type PollParams struct {
	// Question is the question presented to voters.
	Question string
	// Options contains ordered answer labels.
	Options []string
	// Multiple reports whether more than one option may be selected.
	Multiple bool
}

// Poll is an immutable protocol-neutral poll.
type Poll struct {
	question string
	options  []string
	multiple bool
}

// NewPoll validates a poll and defensively owns its options.
func NewPoll(p PollParams) (Poll, error) {
	if len(p.Options) > maxPollOptions || typedStringTooLong(p.Question) {
		return Poll{}, fmt.Errorf("poll: %w", ErrCapacity)
	}
	if !validRichString(p.Question) || len(p.Options) < 2 {
		return Poll{}, fmt.Errorf("poll: %w", ErrInvalid)
	}
	for _, option := range p.Options {
		if typedStringTooLong(option) {
			return Poll{}, fmt.Errorf("poll option: %w", ErrCapacity)
		}
		if !validRichString(option) {
			return Poll{}, fmt.Errorf("poll option: %w", ErrInvalid)
		}
	}
	return Poll{question: p.Question, options: cloneStrings(p.Options), multiple: p.Multiple}, nil
}

// Question returns the poll question.
func (v Poll) Question() string { return v.question }

// Options returns a defensive copy of the ordered answer labels.
func (v Poll) Options() []string { return cloneStrings(v.options) }

// Multiple reports whether more than one option may be selected.
func (v Poll) Multiple() bool { return v.multiple }

// InteractiveParams supplies protocol-neutral layout and action fields.
type InteractiveParams struct {
	// Layout describes how child parts are arranged.
	Layout string
	// ActionID is the optional stable action identifier.
	ActionID string
	// Label is the optional human-readable action label.
	Label string
}

// Interactive is an immutable protocol-neutral layout or action descriptor.
// Its component children are represented by Part.Children.
type Interactive struct {
	layout   string
	actionID string
	label    string
}

// NewInteractive validates a layout and optional action descriptor.
func NewInteractive(p InteractiveParams) (Interactive, error) {
	if typedStringTooLong(p.Layout) || typedStringTooLong(p.ActionID) || typedStringTooLong(p.Label) {
		return Interactive{}, fmt.Errorf("interactive: %w", ErrCapacity)
	}
	if !validRichString(p.Layout) || (p.ActionID == "") != (p.Label == "") || p.ActionID != "" && (!validRichString(p.ActionID) || !validRichString(p.Label)) {
		return Interactive{}, fmt.Errorf("interactive: %w", ErrInvalid)
	}
	return Interactive{layout: p.Layout, actionID: p.ActionID, label: p.Label}, nil
}

// Layout returns the child layout descriptor.
func (v Interactive) Layout() string { return v.layout }

// ActionID returns the optional stable action identifier.
func (v Interactive) ActionID() string { return v.actionID }

// Label returns the optional human-readable action label.
func (v Interactive) Label() string { return v.label }

// EmbeddedMessageParams supplies protocol-neutral embedded-message metadata.
type EmbeddedMessageParams struct {
	// Summary is optional human-readable summary text.
	Summary string
	// Reference is the stable source-message reference.
	Reference string
}

// EmbeddedMessage is immutable protocol-neutral embedded-message metadata.
// Snapshot content is represented by Part.Children.
type EmbeddedMessage struct {
	summary   string
	reference string
}

// NewEmbeddedMessage validates embedded-message metadata.
func NewEmbeddedMessage(p EmbeddedMessageParams) (EmbeddedMessage, error) {
	if typedStringTooLong(p.Summary) || typedStringTooLong(p.Reference) {
		return EmbeddedMessage{}, fmt.Errorf("embedded message: %w", ErrCapacity)
	}
	if !utf8.ValidString(p.Summary) || !validRichString(p.Reference) {
		return EmbeddedMessage{}, fmt.Errorf("embedded message: %w", ErrInvalid)
	}
	return EmbeddedMessage{summary: p.Summary, reference: p.Reference}, nil
}

// Summary returns optional human-readable summary text.
func (v EmbeddedMessage) Summary() string { return v.summary }

// Reference returns the stable source-message reference.
func (v EmbeddedMessage) Reference() string { return v.reference }

// NewLocationPart constructs a location part.
func NewLocationPart(id PartID, location Location, children []PartID, extensions []Extension) (Part, error) {
	if err := validateTypedPart(id, location.valid, children, extensions); err != nil {
		return Part{}, fmt.Errorf("location part: %w", err)
	}
	return Part{id: id, kind: PartLocation, location: location, children: clonePartIDs(children), extensions: cloneExtensions(extensions)}, nil
}

// NewContactPart constructs a contact part.
func NewContactPart(id PartID, contact Contact, children []PartID, extensions []Extension) (Part, error) {
	if err := validateTypedPart(id, contact.displayName != "", children, extensions); err != nil {
		return Part{}, fmt.Errorf("contact part: %w", err)
	}
	return Part{id: id, kind: PartContact, contact: contact, children: clonePartIDs(children), extensions: cloneExtensions(extensions)}, nil
}

// NewPollPart constructs a poll part.
func NewPollPart(id PartID, poll Poll, children []PartID, extensions []Extension) (Part, error) {
	if err := validateTypedPart(id, poll.question != "", children, extensions); err != nil {
		return Part{}, fmt.Errorf("poll part: %w", err)
	}
	return Part{id: id, kind: PartPoll, poll: poll, children: clonePartIDs(children), extensions: cloneExtensions(extensions)}, nil
}

// NewInteractivePart constructs an interactive part whose component tree is held in children.
func NewInteractivePart(id PartID, interactive Interactive, children []PartID, extensions []Extension) (Part, error) {
	if err := validateTypedPart(id, interactive.layout != "", children, extensions); err != nil {
		return Part{}, fmt.Errorf("interactive part: %w", err)
	}
	return Part{id: id, kind: PartInteractive, interactive: interactive, children: clonePartIDs(children), extensions: cloneExtensions(extensions)}, nil
}

// NewEmbeddedMessagePart constructs an embedded-message part whose snapshot is held in children.
func NewEmbeddedMessagePart(id PartID, embedded EmbeddedMessage, children []PartID, extensions []Extension) (Part, error) {
	if err := validateTypedPart(id, embedded.reference != "", children, extensions); err != nil {
		return Part{}, fmt.Errorf("embedded message part: %w", err)
	}
	return Part{id: id, kind: PartEmbeddedMessage, embeddedMessage: embedded, children: clonePartIDs(children), extensions: cloneExtensions(extensions)}, nil
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

// Location returns the location payload and whether this is a location part.
func (v Part) Location() (Location, bool) { return v.location, v.kind == PartLocation }

// Contact returns the contact payload and whether this is a contact part.
func (v Part) Contact() (Contact, bool) { return v.contact, v.kind == PartContact }

// Poll returns the poll payload and whether this is a poll part.
func (v Part) Poll() (Poll, bool) { return v.poll, v.kind == PartPoll }

// Interactive returns the interactive payload and whether this is an interactive part.
func (v Part) Interactive() (Interactive, bool) {
	return v.interactive, v.kind == PartInteractive
}

// EmbeddedMessage returns embedded-message metadata and whether this is an embedded-message part.
func (v Part) EmbeddedMessage() (EmbeddedMessage, bool) {
	return v.embeddedMessage, v.kind == PartEmbeddedMessage
}

// Value returns the opaque codec value when this is an opaque part.
func (v Part) Value() (Extension, bool) { return v.value, v.kind == PartOpaque }
func typedPartKind(kind PartKind) bool {
	switch kind {
	case PartLocation, PartContact, PartPoll, PartInteractive, PartEmbeddedMessage:
		return true
	default:
		return false
	}
}

func validateTypedPart(id PartID, payloadValid bool, children []PartID, extensions []Extension) error {
	if len(children) > 64 || len(extensions) > 64 {
		return ErrCapacity
	}
	if id.value == "" || !payloadValid {
		return ErrInvalid
	}
	return nil
}

func typedStringTooLong(value string) bool { return len(value) > maxTypedPartStringBytes }

func finite(value float64) bool {
	return value == value && value >= -1.7976931348623157e+308 && value <= 1.7976931348623157e+308
}

func cloneStrings(value []string) []string {
	if len(value) == 0 {
		return nil
	}
	out := make([]string, len(value))
	copy(out, value)
	return out
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

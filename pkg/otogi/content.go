package otogi

import "time"

// AnnotationKind says what an annotation marks.
type AnnotationKind string

const (
	// AnnotationBold marks bold text.
	AnnotationBold AnnotationKind = "bold"
	// AnnotationItalic marks italic text.
	AnnotationItalic AnnotationKind = "italic"
	// AnnotationUnderline marks underlined text.
	AnnotationUnderline AnnotationKind = "underline"
	// AnnotationStrike marks struck-through text.
	AnnotationStrike AnnotationKind = "strike"
	// AnnotationSpoiler marks hidden spoiler text.
	AnnotationSpoiler AnnotationKind = "spoiler"
	// AnnotationCode marks inline code.
	AnnotationCode AnnotationKind = "code"
	// AnnotationPreformatted marks a code block; Target holds the language.
	AnnotationPreformatted AnnotationKind = "preformatted"
	// AnnotationBlockQuote marks a quotation.
	AnnotationBlockQuote AnnotationKind = "block_quote"
	// AnnotationLink marks a link; Target holds the URL.
	AnnotationLink AnnotationKind = "link"
	// AnnotationMention marks a mention; Target holds the actor ID when known.
	AnnotationMention AnnotationKind = "mention"
	// AnnotationHashtag marks a hashtag.
	AnnotationHashtag AnnotationKind = "hashtag"
	// AnnotationCommand marks a bot command.
	AnnotationCommand AnnotationKind = "command"
	// AnnotationEmail marks an email address.
	AnnotationEmail AnnotationKind = "email"
	// AnnotationEmoji marks a custom emoji; Target holds its platform ID.
	AnnotationEmoji AnnotationKind = "emoji"
	// AnnotationTimestamp marks a formatted date or time.
	AnnotationTimestamp AnnotationKind = "timestamp"
)

// Annotation marks a byte range of a text part's Text.
type Annotation struct {
	// ID optionally identifies the annotation within its part.
	ID string `json:"id,omitempty"`
	// Offset is the start of the range in bytes.
	Offset int `json:"offset"`
	// Length is the length of the range in bytes.
	Length int `json:"length"`
	// Kind says what the range marks.
	Kind AnnotationKind `json:"kind"`
	// Target is the kind-specific value, such as a URL.
	Target string `json:"target,omitempty"`
	// Extensions carry platform entity data with no portable meaning yet.
	Extensions []Extension `json:"extensions,omitempty"`
}

// MediaRole says what kind of media a part carries.
type MediaRole string

const (
	// MediaImage is a photo or image.
	MediaImage MediaRole = "image"
	// MediaVideo is a video.
	MediaVideo MediaRole = "video"
	// MediaAudio is music or other audio.
	MediaAudio MediaRole = "audio"
	// MediaVoice is a voice note.
	MediaVoice MediaRole = "voice"
	// MediaSticker is a sticker.
	MediaSticker MediaRole = "sticker"
	// MediaAnimation is a GIF or similar animation.
	MediaAnimation MediaRole = "animation"
	// MediaDocument is a document.
	MediaDocument MediaRole = "document"
	// MediaThumbnail is a preview image.
	MediaThumbnail MediaRole = "thumbnail"
	// MediaOther is media with no better role.
	MediaOther MediaRole = "other"
)

// Media describes a media or file payload.
type Media struct {
	// Role says what kind of media this is.
	Role MediaRole `json:"role,omitempty"`
	// MediaType is the MIME type when known.
	MediaType string `json:"media_type,omitempty"`
	// Filename is the original file name when known.
	Filename string `json:"filename,omitempty"`
	// ByteSize is the size in bytes, or zero when unknown.
	ByteSize int64 `json:"byte_size,omitempty"`
	// Width is the pixel width when known.
	Width int `json:"width,omitempty"`
	// Height is the pixel height when known.
	Height int `json:"height,omitempty"`
	// Duration is the playback duration when known.
	Duration time.Duration `json:"duration,omitempty"`
	// Hashes are digests of the content.
	Hashes []MediaHash `json:"hashes,omitempty"`
	// Source says where the bytes are.
	Source MediaSource `json:"source"`
	// Extensions carry platform media data with no portable meaning yet.
	Extensions []Extension `json:"extensions,omitempty"`
}

// MediaHash is a named digest of media content.
type MediaHash struct {
	// Algorithm names the digest, for example "sha256".
	Algorithm string `json:"algorithm"`
	// Value is the digest.
	Value []byte `json:"value"`
}

// MediaSourceKind says how media bytes are obtained.
type MediaSourceKind string

const (
	// MediaSourceInline means the bytes are in Inline.
	MediaSourceInline MediaSourceKind = "inline"
	// MediaSourceExternal means the bytes are at URL.
	MediaSourceExternal MediaSourceKind = "external"
	// MediaSourceOpaque means only the driver can fetch the bytes, using Locator.
	MediaSourceOpaque MediaSourceKind = "opaque"
)

// MediaSource says where media bytes are.
type MediaSource struct {
	// Kind selects the field that locates the bytes.
	Kind MediaSourceKind `json:"kind"`
	// Inline holds small media bytes directly.
	Inline []byte `json:"inline,omitempty"`
	// URL is an external location.
	URL string `json:"url,omitempty"`
	// Locator lets the driver fetch the bytes.
	Locator ProviderLocator `json:"locator,omitzero"`
	// Extensions carry platform source data with no portable meaning yet.
	Extensions []Extension `json:"extensions,omitempty"`
}

// Location is a geographic point.
type Location struct {
	// Latitude in degrees.
	Latitude float64 `json:"latitude"`
	// Longitude in degrees.
	Longitude float64 `json:"longitude"`
	// Label is an optional place name or address.
	Label string `json:"label,omitempty"`
}

// Contact is a shared contact card.
type Contact struct {
	// DisplayName is the contact's name.
	DisplayName string `json:"display_name,omitempty"`
	// Address is a phone number, handle, or other address.
	Address string `json:"address,omitempty"`
}

// Poll is a question with options.
type Poll struct {
	// Question is the poll question.
	Question string `json:"question"`
	// Options are the answer options in order.
	Options []string `json:"options,omitempty"`
	// Multiple says whether several options may be chosen.
	Multiple bool `json:"multiple,omitempty"`
}

// Interactive is a control such as a button or a row of buttons.
type Interactive struct {
	// Layout names the control, for example "row" or "button".
	Layout string `json:"layout,omitempty"`
	// ActionID is the value reported when the control is used.
	ActionID string `json:"action_id,omitempty"`
	// Label is the visible label.
	Label string `json:"label,omitempty"`
}

// EmbeddedMessage is a message embedded in another, such as a quote or a
// MIME sub-part.
type EmbeddedMessage struct {
	// Summary describes the embedded content.
	Summary string `json:"summary,omitempty"`
	// Reference identifies the embedded message when known.
	Reference string `json:"reference,omitempty"`
}

// Extension carries platform data that has no portable meaning, so it is
// preserved rather than dropped.
type Extension struct {
	// Namespace is an absolute URI naming the format.
	Namespace string `json:"namespace"`
	// Name names the element within Namespace.
	Name string `json:"name,omitempty"`
	// Version is the format version.
	Version uint32 `json:"version,omitempty"`
	// MediaType is the MIME type of Data when relevant.
	MediaType string `json:"media_type,omitempty"`
	// Data is the platform data.
	Data []byte `json:"data,omitempty"`
}

// RelationKind says how a message relates to another event.
type RelationKind string

const (
	// RelationReply is a reply to Target.
	RelationReply RelationKind = "reply"
	// RelationThread places the message in Target's thread.
	RelationThread RelationKind = "thread"
	// RelationReplacement is an edit replacing Target.
	RelationReplacement RelationKind = "replacement"
	// RelationAnnotation annotates Target, for example a reaction.
	RelationAnnotation RelationKind = "annotation"
	// RelationReference refers to Target without a stronger meaning.
	RelationReference RelationKind = "reference"
	// RelationForward forwards Target.
	RelationForward RelationKind = "forward"
)

// Relation links a message to another event.
type Relation struct {
	// Kind says how the message relates to Target.
	Kind RelationKind `json:"kind"`
	// Target is the related event.
	Target EventRef `json:"target"`
	// Key is a kind-specific value, such as a reaction emoji.
	Key string `json:"key,omitempty"`
	// Extensions carry platform relation data with no portable meaning yet.
	Extensions []Extension `json:"extensions,omitempty"`
}

// EventRef points at an event on a platform.
type EventRef struct {
	// Platform is the target's platform.
	Platform PlatformID `json:"platform,omitempty"`
	// Account is the account that observed the target.
	Account AccountID `json:"account,omitempty"`
	// Conversation is the target's conversation.
	Conversation ConversationID `json:"conversation,omitempty"`
	// SourceEvent is the target's platform ID.
	SourceEvent SourceEventID `json:"source_event"`
	// Locator lets the driver find the target again.
	Locator ProviderLocator `json:"locator,omitzero"`
}

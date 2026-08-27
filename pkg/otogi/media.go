package otogi

import (
	"fmt"
	"time"
	"unicode/utf8"
)

// MediaRole identifies a media item's portable semantic role.
type MediaRole string

const (
	// MediaImage is still image media.
	MediaImage MediaRole = "image"
	// MediaVideo is video media.
	MediaVideo MediaRole = "video"
	// MediaAudio is general audio media.
	MediaAudio MediaRole = "audio"
	// MediaVoice is a voice message.
	MediaVoice MediaRole = "voice"
	// MediaSticker is sticker media.
	MediaSticker MediaRole = "sticker"
	// MediaAnimation is animated media.
	MediaAnimation MediaRole = "animation"
	// MediaDocument is document media.
	MediaDocument MediaRole = "document"
	// MediaThumbnail is thumbnail media.
	MediaThumbnail MediaRole = "thumbnail"
	// MediaOther is media with no more specific portable role.
	MediaOther MediaRole = "other"
)

// MediaHash is an immutable named digest.
type MediaHash struct {
	algorithm string
	value     []byte
}

// NewMediaHash validates and defensively owns a digest.
func NewMediaHash(algorithm string, value []byte) (MediaHash, error) {
	if len(value) > maxNormalizedMetadata {
		return MediaHash{}, fmt.Errorf("media hash: %w", ErrCapacity)
	}
	if !validRichString(algorithm) || len(value) == 0 {
		return MediaHash{}, fmt.Errorf("media hash: %w", ErrInvalid)
	}
	return MediaHash{algorithm: algorithm, value: cloneBytes(value)}, nil
}

// Algorithm returns the digest algorithm name.
func (v MediaHash) Algorithm() string { return v.algorithm }

// Value returns a defensive copy of the digest bytes.
func (v MediaHash) Value() []byte { return cloneBytes(v.value) }

// MediaSourceKind identifies a media source variant.
type MediaSourceKind string

const (
	// MediaSourceInline stores bytes directly.
	MediaSourceInline MediaSourceKind = "inline"
	// MediaSourceExternal identifies bytes by absolute URI.
	MediaSourceExternal MediaSourceKind = "external"
	// MediaSourceOpaque stores a provider-owned opaque locator.
	MediaSourceOpaque MediaSourceKind = "opaque"
)

// MediaSource is an immutable exactly-one media source value.
type MediaSource struct {
	kind       MediaSourceKind
	inline     []byte
	external   string
	locator    ProviderLocator
	extensions []Extension
}

// NewInlineMediaSource owns bounded inline media bytes.
func NewInlineMediaSource(data []byte, extensions []Extension) (MediaSource, error) {
	if len(data) > 256*1024 {
		return MediaSource{}, fmt.Errorf("inline media: %w", ErrCapacity)
	}
	if len(extensions) > 64 {
		return MediaSource{}, fmt.Errorf("inline media extensions: %w", ErrCapacity)
	}
	if len(data) == 0 {
		return MediaSource{}, fmt.Errorf("inline media source: %w", ErrInvalid)
	}
	return MediaSource{kind: MediaSourceInline, inline: cloneBytes(data), extensions: cloneExtensions(extensions)}, nil
}

// NewExternalMediaSource validates an absolute URI.
func NewExternalMediaSource(uri string, extensions []Extension) (MediaSource, error) {
	if len(extensions) > 64 {
		return MediaSource{}, fmt.Errorf("external media extensions: %w", ErrCapacity)
	}
	if !validAbsoluteURI(uri) {
		return MediaSource{}, fmt.Errorf("external media source: %w", ErrInvalid)
	}
	return MediaSource{kind: MediaSourceExternal, external: uri, extensions: cloneExtensions(extensions)}, nil
}

// NewOpaqueMediaSource stores a provider-owned opaque locator.
func NewOpaqueMediaSource(locator ProviderLocator, extensions []Extension) (MediaSource, error) {
	if len(extensions) > 64 {
		return MediaSource{}, fmt.Errorf("opaque media extensions: %w", ErrCapacity)
	}
	if locator.namespaceURI == "" {
		return MediaSource{}, fmt.Errorf("opaque media source: %w", ErrInvalid)
	}
	return MediaSource{kind: MediaSourceOpaque, locator: locator, extensions: cloneExtensions(extensions)}, nil
}

// Kind returns the source variant.
func (v MediaSource) Kind() MediaSourceKind { return v.kind }

// Inline returns copied inline bytes and whether this is the inline variant.
func (v MediaSource) Inline() ([]byte, bool) {
	return cloneBytes(v.inline), v.kind == MediaSourceInline
}

// ExternalURI returns the URI and whether this is the external variant.
func (v MediaSource) ExternalURI() (string, bool) { return v.external, v.kind == MediaSourceExternal }

// OpaqueLocator returns the locator and whether this is the opaque variant.
func (v MediaSource) OpaqueLocator() (ProviderLocator, bool) {
	return v.locator, v.kind == MediaSourceOpaque
}

// Extensions returns ordered source extensions.
func (v MediaSource) Extensions() []Extension { return cloneExtensions(v.extensions) }

// MediaParams supplies protocol-neutral media metadata.
type MediaParams struct {
	// Role identifies the portable semantic role.
	Role MediaRole
	// MediaType is the declared media type.
	MediaType string
	// Filename is the optional original filename.
	Filename string
	// ByteSize is the nonnegative byte size, or zero when unknown.
	ByteSize int64
	// Width is the optional nonnegative pixel width.
	Width int
	// Height is the optional nonnegative pixel height.
	Height int
	// Duration is the optional nonnegative playback duration.
	Duration time.Duration
	// Hashes contains ordered named digests.
	Hashes []MediaHash
	// Source is the exactly-one media source.
	Source MediaSource
	// Extensions contains ordered media metadata extensions.
	Extensions []Extension
}

// Media is immutable protocol-neutral media metadata with exactly one source.
type Media struct {
	role                MediaRole
	mediaType, filename string
	byteSize            int64
	width, height       int
	duration            time.Duration
	hashes              []MediaHash
	source              MediaSource
	extensions          []Extension
}

// NewMedia validates metadata and defensively owns collections.
func NewMedia(p MediaParams) (Media, error) {
	if len(p.Hashes) > 32 || len(p.Extensions) > 64 {
		return Media{}, fmt.Errorf("media collections: %w", ErrCapacity)
	}
	if !validMediaRole(p.Role) || !validRichString(p.MediaType) || !utf8.ValidString(p.Filename) || p.ByteSize < 0 || p.Width < 0 || p.Height < 0 || p.Duration < 0 || p.Source.kind == "" {
		return Media{}, fmt.Errorf("media: %w", ErrInvalid)
	}
	for _, hash := range p.Hashes {
		if hash.algorithm == "" || len(hash.value) == 0 {
			return Media{}, fmt.Errorf("media hash: %w", ErrInvalid)
		}
	}
	hashes := make([]MediaHash, len(p.Hashes))
	copy(hashes, p.Hashes)
	return Media{role: p.Role, mediaType: p.MediaType, filename: p.Filename, byteSize: p.ByteSize, width: p.Width, height: p.Height, duration: p.Duration, hashes: hashes, source: p.Source, extensions: cloneExtensions(p.Extensions)}, nil
}

// Role returns the semantic media role.
func (v Media) Role() MediaRole { return v.role }

// MediaType returns the declared media type.
func (v Media) MediaType() string { return v.mediaType }

// Filename returns the optional filename.
func (v Media) Filename() string { return v.filename }

// ByteSize returns the byte size, or zero when unknown.
func (v Media) ByteSize() int64 { return v.byteSize }

// Width returns the width, or zero when absent.
func (v Media) Width() int { return v.width }

// Height returns the height, or zero when absent.
func (v Media) Height() int { return v.height }

// Duration returns the duration, or zero when absent.
func (v Media) Duration() time.Duration { return v.duration }

// Hashes returns ordered immutable hash values.
func (v Media) Hashes() []MediaHash {
	out := make([]MediaHash, len(v.hashes))
	copy(out, v.hashes)
	return out
}

// Source returns the exactly-one media source.
func (v Media) Source() MediaSource { return v.source }

// Extensions returns ordered media extensions.
func (v Media) Extensions() []Extension { return cloneExtensions(v.extensions) }

func validMediaRole(role MediaRole) bool {
	switch role {
	case MediaImage, MediaVideo, MediaAudio, MediaVoice, MediaSticker, MediaAnimation, MediaDocument, MediaThumbnail, MediaOther:
		return true
	default:
		return false
	}
}

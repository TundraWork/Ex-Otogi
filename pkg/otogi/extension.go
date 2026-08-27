package otogi

import (
	"fmt"
	"net/url"
	"unicode/utf8"
)

const (
	maxExtensionBytes  = 64 * 1024
	maxLocatorBytes    = 16 * 1024
	maxOwnerExtensions = 64
)

// Extension is an immutable namespaced preservation record.
type Extension struct {
	namespaceURI  string
	localName     string
	schemaVersion uint32
	mediaType     string
	data          []byte
}

// NewExtension validates metadata and defensively owns data.
func NewExtension(namespaceURI, localName string, schemaVersion uint32, mediaType string, data []byte) (Extension, error) {
	if len(data) > maxExtensionBytes {
		return Extension{}, fmt.Errorf("extension data: %w", ErrCapacity)
	}
	if !validAbsoluteURI(namespaceURI) || !validRichString(localName) || schemaVersion == 0 || !validRichString(mediaType) {
		return Extension{}, fmt.Errorf("extension: %w", ErrInvalid)
	}
	return Extension{namespaceURI: namespaceURI, localName: localName, schemaVersion: schemaVersion, mediaType: mediaType, data: cloneBytes(data)}, nil
}

// NamespaceURI returns the absolute namespace URI.
func (v Extension) NamespaceURI() string { return v.namespaceURI }

// LocalName returns the extension's namespace-local type name.
func (v Extension) LocalName() string { return v.localName }

// SchemaVersion returns the extension schema version.
func (v Extension) SchemaVersion() uint32 { return v.schemaVersion }

// MediaType returns the media type describing Data.
func (v Extension) MediaType() string { return v.mediaType }

// Data returns a defensive copy of the preserved bytes.
func (v Extension) Data() []byte { return cloneBytes(v.data) }

// ProviderLocator is an immutable namespaced provider-owned locator.
type ProviderLocator struct {
	namespaceURI  string
	schemaVersion uint32
	data          []byte
}

// NewProviderLocator validates metadata and defensively owns data.
func NewProviderLocator(namespaceURI string, schemaVersion uint32, data []byte) (ProviderLocator, error) {
	if len(data) > maxLocatorBytes {
		return ProviderLocator{}, fmt.Errorf("provider locator data: %w", ErrCapacity)
	}
	if !validAbsoluteURI(namespaceURI) || schemaVersion == 0 {
		return ProviderLocator{}, fmt.Errorf("provider locator: %w", ErrInvalid)
	}
	return ProviderLocator{namespaceURI: namespaceURI, schemaVersion: schemaVersion, data: cloneBytes(data)}, nil
}

// NamespaceURI returns the absolute locator namespace URI.
func (v ProviderLocator) NamespaceURI() string { return v.namespaceURI }

// SchemaVersion returns the locator schema version.
func (v ProviderLocator) SchemaVersion() uint32 { return v.schemaVersion }

// Data returns a defensive copy of the opaque locator bytes.
func (v ProviderLocator) Data() []byte { return cloneBytes(v.data) }

func validExtension(v Extension) bool {
	return len(v.data) <= maxExtensionBytes && validAbsoluteURI(v.namespaceURI) && validRichString(v.localName) && v.schemaVersion != 0 && validRichString(v.mediaType)
}

func validAbsoluteURI(value string) bool {
	if value == "" || !utf8.ValidString(value) {
		return false
	}
	u, err := url.Parse(value)
	return err == nil && u.IsAbs()
}

func validRichString(value string) bool { return value != "" && utf8.ValidString(value) }

func cloneBytes(value []byte) []byte {
	if len(value) == 0 {
		return nil
	}
	out := make([]byte, len(value))
	copy(out, value)
	return out
}

func cloneExtensions(value []Extension) []Extension {
	if len(value) == 0 {
		return nil
	}
	out := make([]Extension, len(value))
	copy(out, value)
	return out
}

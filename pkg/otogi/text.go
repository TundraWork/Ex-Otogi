package otogi

import (
	"fmt"
	"unicode/utf8"
)

// Text is exact nonempty UTF-8 message content of at most 4096 bytes.
type Text struct{ value string }

// NewText preserves valid content exactly and rejects empty, invalid, or oversized content.
func NewText(value string) (Text, error) {
	if value == "" || len(value) > 4096 || !utf8.ValidString(value) {
		return Text{}, fmt.Errorf("text: %w", ErrInvalid)
	}
	return Text{value}, nil
}

// String returns the exact validated text content.
func (v Text) String() string { return v.value }

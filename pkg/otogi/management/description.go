package management

import "strings"

const maxDescriptionRunes = 140

// TruncateDescription normalizes whitespace in s and truncates it to
// maxDescriptionRunes runes with an ellipsis suffix when the content exceeds
// the limit. It returns an empty string for blank input.
func TruncateDescription(s string) string {
	// Normalize whitespace: collapse runs of whitespace into single spaces.
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxDescriptionRunes {
		return s
	}
	return string(runes[:maxDescriptionRunes]) + "..."
}

package memory

import (
	"strings"
	"unicode/utf8"
)

func trimRunesWithEllipsis(raw string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}

	runes := []rune(raw)
	if len(runes) <= maxRunes {
		return raw
	}
	if maxRunes <= 3 {
		return strings.Repeat(".", maxRunes)
	}

	return string(runes[:maxRunes-3]) + "..."
}

func runeCount(value string) int {
	return utf8.RuneCountInString(value)
}

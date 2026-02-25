package llmchat

import (
	"strings"
	"unicode"
)

func matchAgentTrigger(text string, agentName string) (prompt string, matched bool) {
	trimmedText := strings.TrimSpace(text)
	trimmedName := strings.TrimSpace(agentName)
	if trimmedText == "" || trimmedName == "" {
		return "", false
	}

	textRunes := []rune(trimmedText)
	nameRunes := []rune(trimmedName)
	if len(textRunes) < len(nameRunes) {
		return "", false
	}
	if !strings.EqualFold(string(textRunes[:len(nameRunes)]), trimmedName) {
		return "", false
	}

	if len(textRunes) == len(nameRunes) {
		return "", true
	}

	next := textRunes[len(nameRunes)]
	if !isAgentWordBoundary(next) {
		return "", false
	}

	rest := strings.TrimSpace(string(textRunes[len(nameRunes):]))
	rest = strings.TrimLeft(rest, ":,.;!?，。：；！？")
	rest = strings.TrimSpace(rest)

	return rest, true
}

func isAgentWordBoundary(r rune) bool {
	if unicode.IsSpace(r) {
		return true
	}
	if unicode.IsPunct(r) {
		return true
	}

	return false
}

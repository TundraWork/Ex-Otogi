package discord

import (
	"sort"
	"strings"

	"ex-otogi/pkg/otogi/platform"
)

const (
	discordMaxMessageLength = 2000
	discordTruncationSuffix = "... [truncated]"
)

// renderDiscordMarkdown converts TextEntity-annotated text to Discord Markdown.
//
// Inline entities (bold, italic, underline, strikethrough, spoiler, code, pre,
// text_url) are rendered by inserting Markdown delimiters at entity boundaries.
// Outer entities open before inner entities and close after inner entities, which
// preserves correct Markdown nesting. MentionName entities replace the entity span
// with a `<@id>` Discord mention token.
func renderDiscordMarkdown(text string, entities []platform.TextEntity) string {
	if text == "" {
		return ""
	}
	if len(entities) == 0 {
		return text
	}

	runes := []rune(text)
	n := len(runes)

	type event struct {
		tag     string
		length  int
		isClose bool
	}

	// openAt and closeAt store events indexed by rune position.
	openAt := make([][]event, n+1)
	closeAt := make([][]event, n+1)
	// skipRune marks rune positions that belong to a replacement span.
	skipRune := make([]bool, n)

	for _, entity := range entities {
		start := entity.Offset
		end := entity.Offset + entity.Length
		if start < 0 || end > n || start >= end {
			continue
		}

		if entity.Type == platform.TextEntityTypeMentionName {
			// Replace entire span with <@userID>.
			openAt[start] = append(openAt[start], event{
				tag:    "<@" + entity.MentionUserID + ">",
				length: entity.Length,
			})
			for i := start; i < end; i++ {
				skipRune[i] = true
			}

			continue
		}

		open, closeDelim := discordEntityDelimiters(entity)
		if open == "" && closeDelim == "" {
			continue
		}

		openAt[start] = append(openAt[start], event{tag: open, length: entity.Length})
		if closeDelim != "" {
			closeAt[end] = append(closeAt[end], event{tag: closeDelim, length: entity.Length, isClose: true})
		}
	}

	// Sort open events: outer first (larger length).
	// Sort close events: inner first (smaller length).
	for i := range openAt {
		if len(openAt[i]) > 1 {
			sort.Slice(openAt[i], func(a, b int) bool {
				return openAt[i][a].length > openAt[i][b].length
			})
		}
	}
	for i := range closeAt {
		if len(closeAt[i]) > 1 {
			sort.Slice(closeAt[i], func(a, b int) bool {
				return closeAt[i][a].length < closeAt[i][b].length
			})
		}
	}

	var sb strings.Builder
	sb.Grow(len(text) + len(entities)*4)

	for pos := 0; pos <= n; pos++ {
		for _, ev := range closeAt[pos] {
			sb.WriteString(ev.tag)
		}
		for _, ev := range openAt[pos] {
			sb.WriteString(ev.tag)
		}
		if pos < n && !skipRune[pos] {
			sb.WriteRune(runes[pos])
		}
	}

	return sb.String()
}

// discordEntityDelimiters returns the open and close Markdown delimiters for
// the given TextEntity. Returns two empty strings for entity types that have
// no supported Discord Markdown representation.
func discordEntityDelimiters(entity platform.TextEntity) (open, closeDelim string) {
	switch entity.Type {
	case platform.TextEntityTypeBold:
		return "**", "**"
	case platform.TextEntityTypeItalic:
		return "_", "_"
	case platform.TextEntityTypeUnderline:
		return "__", "__"
	case platform.TextEntityTypeStrike:
		return "~~", "~~"
	case platform.TextEntityTypeSpoiler:
		return "||", "||"
	case platform.TextEntityTypeCode:
		return "`", "`"
	case platform.TextEntityTypePre:
		return "```" + entity.Language + "\n", "\n```"
	case platform.TextEntityTypeTextURL:
		return "[", "](" + entity.URL + ")"
	case platform.TextEntityTypeBlockquote:
		// Simplified: insert block-quote prefix at span start.
		return "> ", ""
	default:
		return "", ""
	}
}

// splitDiscordMessage splits text into chunks no longer than discordMaxMessageLength
// runes. Splits prefer the last newline before the limit; falls back to a hard cut.
func splitDiscordMessage(text string) []string {
	const splitLimit = discordMaxMessageLength - 10 // leave margin for safety

	runes := []rune(text)
	if len(runes) <= discordMaxMessageLength {
		return []string{text}
	}

	var parts []string
	for len(runes) > 0 {
		if len(runes) <= discordMaxMessageLength {
			parts = append(parts, string(runes))
			break
		}

		cut := splitLimit
		// Look for the last newline before the cut point.
		for cut > 0 && runes[cut] != '\n' {
			cut--
		}
		if cut == 0 {
			// No newline found; hard cut at limit.
			cut = discordMaxMessageLength
		} else {
			// Include the newline in the first part.
			cut++
		}

		parts = append(parts, string(runes[:cut]))
		runes = runes[cut:]
	}

	return parts
}

// truncateDiscordEdit truncates text to discordMaxMessageLength runes, appending
// discordTruncationSuffix when truncation occurs.
func truncateDiscordEdit(text string) string {
	runes := []rune(text)
	if len(runes) <= discordMaxMessageLength {
		return text
	}

	suffixRunes := []rune(discordTruncationSuffix)
	limit := discordMaxMessageLength - len(suffixRunes)
	if limit < 0 {
		limit = 0
	}

	return string(runes[:limit]) + discordTruncationSuffix
}

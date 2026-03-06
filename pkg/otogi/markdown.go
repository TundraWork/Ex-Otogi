package otogi

import "context"

// ServiceMarkdownParser is the canonical service registry key for markdown parsing.
const ServiceMarkdownParser = "otogi.markdown_parser"

// MarkdownParser converts markdown text into normalized text plus rich-text entities.
//
// Implementations must be concurrency-safe because modules can parse text from
// multiple workers concurrently.
type MarkdownParser interface {
	// ParseMarkdown parses markdown and returns normalized text plus entity spans.
	//
	// Returned entities are aligned to rune offsets in ParsedText.Text.
	ParseMarkdown(ctx context.Context, markdown string) (ParsedText, error)
}

// ParsedText stores parser output text and aligned rich-text entities.
type ParsedText struct {
	// Text is the normalized plain text representation of markdown input.
	Text string
	// Entities decorates Text with formatting and structural spans.
	Entities []TextEntity
}

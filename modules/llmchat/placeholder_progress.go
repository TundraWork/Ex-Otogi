package llmchat

import (
	"encoding/json"
	"fmt"
	"strings"

	"ex-otogi/pkg/otogi/ai"
)

const (
	defaultThinkingBrief   = "Reviewing your request and planning the response."
	defaultToolStatusBrief = "Running a tool needed to finish the response."
	maxPlaceholderRunes    = 180
)

const defaultThinkingPlaceholder = "Step 1: Thinking\n\n" + defaultThinkingBrief

type placeholderStageKind string

const (
	placeholderStageThinking placeholderStageKind = "thinking"
	placeholderStageTool     placeholderStageKind = "tool"
)

type placeholderProgress struct {
	step              int
	stage             placeholderStageKind
	thinkingIteration int
}

func newPlaceholderProgress() *placeholderProgress {
	return &placeholderProgress{
		step:              1,
		stage:             placeholderStageThinking,
		thinkingIteration: 1,
	}
}

func (p *placeholderProgress) RenderThinking(iteration int, rawSummary string) string {
	if p == nil {
		return renderThinkingPlaceholder(1, rawSummary)
	}

	if iteration < 1 {
		iteration = 1
	}
	if p.stage != placeholderStageThinking || p.thinkingIteration != iteration {
		p.step++
		p.stage = placeholderStageThinking
		p.thinkingIteration = iteration
	}

	return renderThinkingPlaceholder(p.step, rawSummary)
}

func (p *placeholderProgress) RenderTool(toolCall ai.LLMToolCall, definition ai.LLMToolDefinition) string {
	if p == nil {
		return renderToolPlaceholder(1, toolCall, definition)
	}

	p.step++
	p.stage = placeholderStageTool

	return renderToolPlaceholder(p.step, toolCall, definition)
}

func renderThinkingPlaceholder(step int, rawSummary string) string {
	brief := shapeThinkingSummary(rawSummary)
	if brief == "" {
		brief = defaultThinkingBrief
	}

	return renderPlaceholderStatus(step, "Thinking", brief)
}

func renderToolPlaceholder(step int, toolCall ai.LLMToolCall, definition ai.LLMToolDefinition) string {
	titleSuffix := "Using tool"
	if toolName := humanizeToolName(toolCall.Name); toolName != "" {
		titleSuffix = "Using " + toolName
	}

	return renderPlaceholderStatus(
		step,
		titleSuffix,
		shapeToolCallPlaceholderBrief(toolCall, definition.Description),
	)
}

func renderPlaceholderStatus(step int, titleSuffix string, brief string) string {
	if step < 1 {
		step = 1
	}
	titleSuffix = strings.TrimSpace(titleSuffix)
	if titleSuffix == "" {
		titleSuffix = "Working"
	}
	brief = strings.TrimSpace(brief)
	if brief == "" {
		brief = defaultThinkingBrief
	}

	return fmt.Sprintf("Step %d: %s\n\n%s", step, titleSuffix, brief)
}

func shapeThinkingSummary(raw string) string {
	normalized := strings.Join(strings.Fields(raw), " ")
	if normalized == "" {
		return ""
	}

	return trimRunesWithEllipsis(normalized, maxPlaceholderRunes)
}

func shapeToolCallPlaceholderBrief(toolCall ai.LLMToolCall, description string) string {
	toolName := strings.TrimSpace(toolCall.Name)
	toolNameLower := strings.ToLower(toolName)
	args := parseToolCallArguments(toolCall.Arguments)

	query := firstNonEmptyString(args, "query", "search", "topic", "subject")
	if query != "" {
		query = quotePlaceholderValue(query)
		if strings.Contains(toolNameLower, "search") {
			return fmt.Sprintf("Searching for %s", query)
		}
		return fmt.Sprintf("Using %s for %s", renderToolNameForSentence(toolName), query)
	}

	url := firstNonEmptyString(args, "url", "link")
	if url != "" {
		url = trimRunesWithEllipsis(strings.TrimSpace(url), maxPlaceholderRunes/2)
		question := firstNonEmptyString(args, "question")
		if question != "" {
			return fmt.Sprintf("Reading %s to answer %s", url, quotePlaceholderValue(question))
		}
		return fmt.Sprintf("Reading %s", url)
	}

	content := firstNonEmptyString(args, "content", "text", "note", "memory")
	if content != "" {
		content = quotePlaceholderValue(content)
		if strings.Contains(toolNameLower, "remember") || strings.Contains(strings.ToLower(description), "persist") || strings.Contains(strings.ToLower(description), "save") {
			return fmt.Sprintf("Saving %s for later use", content)
		}
		return fmt.Sprintf("Processing %s", content)
	}

	question := firstNonEmptyString(args, "question", "prompt")
	if question != "" {
		return fmt.Sprintf("Working on %s", quotePlaceholderValue(question))
	}

	description = strings.Join(strings.Fields(description), " ")
	if description != "" {
		return trimRunesWithEllipsis(description, maxPlaceholderRunes)
	}

	if toolName != "" {
		return "Running " + renderToolNameForSentence(toolName)
	}

	return defaultToolStatusBrief
}

func parseToolCallArguments(raw string) map[string]any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil
	}

	return payload
}

func firstNonEmptyString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := payload[strings.TrimSpace(key)]
		if !ok {
			continue
		}

		text, ok := value.(string)
		if !ok {
			continue
		}
		text = strings.Join(strings.Fields(text), " ")
		if text == "" {
			continue
		}

		return trimRunesWithEllipsis(text, maxPlaceholderRunes/2)
	}

	return ""
}

func humanizeToolName(name string) string {
	replacer := strings.NewReplacer("_", " ", "-", " ", ".", " ")
	return strings.Join(strings.Fields(replacer.Replace(strings.TrimSpace(name))), " ")
}

func renderToolNameForSentence(name string) string {
	humanized := humanizeToolName(name)
	if humanized == "" {
		return "this tool"
	}

	return humanized
}

func quotePlaceholderValue(value string) string {
	value = trimRunesWithEllipsis(strings.Join(strings.Fields(value), " "), maxPlaceholderRunes/2)
	if value == "" {
		return `""`
	}

	return fmt.Sprintf("%q", value)
}

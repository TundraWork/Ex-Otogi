package telegram

import (
	"fmt"
	"sort"
	"strings"

	"ex-otogi/pkg/otogi"
)

const telegramReadableThematicSeparator = "----------------"

type telegramReadableRenderResult struct {
	renderedText     string
	renderedEntities []otogi.TextEntity
}

type telegramTextRewriteOp struct {
	start       int
	end         int
	replacement string
	reason      string
}

type telegramTextLine struct {
	start   int
	end     int
	content string
}

func renderTelegramReadableText(text string, entities []otogi.TextEntity) (telegramReadableRenderResult, error) {
	renderedText := text
	renderedEntities := cloneTelegramTextEntities(entities)

	newlineOps := buildTelegramNewlineRewriteOps(renderedText)
	var err error
	renderedText, renderedEntities, err = applyTelegramRewritePass(renderedText, renderedEntities, newlineOps)
	if err != nil {
		return telegramReadableRenderResult{}, fmt.Errorf("normalize newline pass: %w", err)
	}

	structuralOps, err := buildTelegramStructuralRewriteOps(renderedText, renderedEntities)
	if err != nil {
		return telegramReadableRenderResult{}, fmt.Errorf("build structural rewrite ops: %w", err)
	}
	renderedText, renderedEntities, err = applyTelegramRewritePass(renderedText, renderedEntities, structuralOps)
	if err != nil {
		return telegramReadableRenderResult{}, fmt.Errorf("apply structural rewrite ops: %w", err)
	}

	trailingSpaceOps := buildTelegramTrailingSpaceRewriteOps(renderedText)
	renderedText, renderedEntities, err = applyTelegramRewritePass(renderedText, renderedEntities, trailingSpaceOps)
	if err != nil {
		return telegramReadableRenderResult{}, fmt.Errorf("trim trailing spaces: %w", err)
	}

	edgeTrimOps := buildTelegramEdgeNewlineTrimOps(renderedText)
	renderedText, renderedEntities, err = applyTelegramRewritePass(renderedText, renderedEntities, edgeTrimOps)
	if err != nil {
		return telegramReadableRenderResult{}, fmt.Errorf("trim edge newlines: %w", err)
	}

	collapseOps := buildTelegramCollapseNewlineRewriteOps(renderedText)
	renderedText, renderedEntities, err = applyTelegramRewritePass(renderedText, renderedEntities, collapseOps)
	if err != nil {
		return telegramReadableRenderResult{}, fmt.Errorf("collapse newline runs: %w", err)
	}

	if err := otogi.ValidateTextEntities(renderedText, renderedEntities); err != nil {
		return telegramReadableRenderResult{}, fmt.Errorf("validate rendered entities: %w", err)
	}

	return telegramReadableRenderResult{
		renderedText:     renderedText,
		renderedEntities: renderedEntities,
	}, nil
}

func applyTelegramRewritePass(
	text string,
	entities []otogi.TextEntity,
	ops []telegramTextRewriteOp,
) (string, []otogi.TextEntity, error) {
	if len(ops) == 0 {
		return text, entities, nil
	}

	rewrittenText, oldToNewBoundary, err := applyTelegramRewriteOps(text, ops)
	if err != nil {
		return "", nil, err
	}
	rewrittenEntities, err := remapTelegramTextEntities(entities, oldToNewBoundary)
	if err != nil {
		return "", nil, err
	}

	return rewrittenText, rewrittenEntities, nil
}

func applyTelegramRewriteOps(text string, ops []telegramTextRewriteOp) (string, []int, error) {
	sourceRunes := []rune(text)
	identity := make([]int, len(sourceRunes)+1)
	for index := range identity {
		identity[index] = index
	}
	if len(ops) == 0 {
		return text, identity, nil
	}

	sortedOps := append([]telegramTextRewriteOp(nil), ops...)
	sort.SliceStable(sortedOps, func(left int, right int) bool {
		if sortedOps[left].start == sortedOps[right].start {
			return sortedOps[left].end < sortedOps[right].end
		}
		return sortedOps[left].start < sortedOps[right].start
	})

	for index, op := range sortedOps {
		if op.start < 0 || op.end < 0 || op.start >= op.end || op.end > len(sourceRunes) {
			return "", nil, fmt.Errorf(
				"rewrite op[%d] has invalid range [%d,%d) for rune length %d",
				index,
				op.start,
				op.end,
				len(sourceRunes),
			)
		}
		if index > 0 {
			previous := sortedOps[index-1]
			if op.start < previous.end {
				return "", nil, fmt.Errorf(
					"rewrite op[%d] overlaps previous op[%d]: [%d,%d) with [%d,%d)",
					index,
					index-1,
					op.start,
					op.end,
					previous.start,
					previous.end,
				)
			}
		}
	}

	rewrittenRunes := make([]rune, 0, len(sourceRunes))
	mapping := make([]int, len(sourceRunes)+1)

	oldPos := 0
	newPos := 0
	for _, op := range sortedOps {
		for boundary := oldPos; boundary <= op.start; boundary++ {
			mapping[boundary] = newPos + (boundary - oldPos)
		}
		rewrittenRunes = append(rewrittenRunes, sourceRunes[oldPos:op.start]...)
		newPos += op.start - oldPos

		replacementRunes := []rune(op.replacement)
		replacementStart := newPos
		replacementEnd := replacementStart + len(replacementRunes)
		mapping[op.start] = replacementStart
		for boundary := op.start + 1; boundary <= op.end; boundary++ {
			mapping[boundary] = replacementEnd
		}
		rewrittenRunes = append(rewrittenRunes, replacementRunes...)
		newPos = replacementEnd
		oldPos = op.end
	}

	for boundary := oldPos; boundary <= len(sourceRunes); boundary++ {
		mapping[boundary] = newPos + (boundary - oldPos)
	}
	rewrittenRunes = append(rewrittenRunes, sourceRunes[oldPos:]...)

	return string(rewrittenRunes), mapping, nil
}

func remapTelegramTextEntities(entities []otogi.TextEntity, oldToNewBoundary []int) ([]otogi.TextEntity, error) {
	if len(entities) == 0 {
		return nil, nil
	}

	rewritten := make([]otogi.TextEntity, 0, len(entities))
	for index, entity := range entities {
		start := entity.Offset
		end := entity.Offset + entity.Length
		if start < 0 || end < start || end >= len(oldToNewBoundary) {
			return nil, fmt.Errorf(
				"entity[%d] has invalid range [%d,%d) for boundary map length %d",
				index,
				start,
				end,
				len(oldToNewBoundary),
			)
		}

		newStart := oldToNewBoundary[start]
		newEnd := oldToNewBoundary[end]
		if newEnd < newStart {
			return nil, fmt.Errorf(
				"entity[%d] remapped to invalid range [%d,%d)",
				index,
				newStart,
				newEnd,
			)
		}
		if newEnd == newStart {
			if isTelegramUnsupportedMarkdownEntityType(normalizeEntityType(entity.Type)) {
				continue
			}
			return nil, fmt.Errorf(
				"entity[%d] of type %q collapsed to empty range during rewrite",
				index,
				entity.Type,
			)
		}

		entity.Offset = newStart
		entity.Length = newEnd - newStart
		rewritten = append(rewritten, entity)
	}

	return rewritten, nil
}

func cloneTelegramTextEntities(entities []otogi.TextEntity) []otogi.TextEntity {
	if len(entities) == 0 {
		return nil
	}
	cloned := make([]otogi.TextEntity, len(entities))
	copy(cloned, entities)
	return cloned
}

func buildTelegramNewlineRewriteOps(text string) []telegramTextRewriteOp {
	runes := []rune(text)
	ops := make([]telegramTextRewriteOp, 0)
	for index := 0; index < len(runes); {
		if runes[index] != '\r' {
			index++
			continue
		}

		if index+1 < len(runes) && runes[index+1] == '\n' {
			ops = append(ops, telegramTextRewriteOp{
				start:       index,
				end:         index + 2,
				replacement: "\n",
				reason:      "normalize_crlf",
			})
			index += 2
			continue
		}

		ops = append(ops, telegramTextRewriteOp{
			start:       index,
			end:         index + 1,
			replacement: "\n",
			reason:      "normalize_cr",
		})
		index++
	}

	return ops
}

func buildTelegramStructuralRewriteOps(
	text string,
	entities []otogi.TextEntity,
) ([]telegramTextRewriteOp, error) {
	runes := []rune(text)
	lines := indexTelegramTextLines(runes)

	headingLines := make(map[int]struct{})
	thematicLines := make(map[int]struct{})
	tableRowLines := make(map[int]struct{})
	ops := make([]telegramTextRewriteOp, 0)

	for index, entity := range entities {
		typ := normalizeEntityType(entity.Type)
		if typ == otogi.TextEntityTypeHeading {
			lineIndex, err := telegramLineIndexForOffset(lines, entity.Offset)
			if err != nil {
				return nil, fmt.Errorf("entity[%d] heading line lookup: %w", index, err)
			}
			headingLines[lineIndex] = struct{}{}
			continue
		}
		if typ == otogi.TextEntityTypeThematicBreak {
			lineIndex, err := telegramLineIndexForOffset(lines, entity.Offset)
			if err != nil {
				return nil, fmt.Errorf("entity[%d] thematic line lookup: %w", index, err)
			}
			thematicLines[lineIndex] = struct{}{}
			continue
		}
		if typ == otogi.TextEntityTypeTableRow {
			lineIndex, err := telegramLineIndexForOffset(lines, entity.Offset)
			if err != nil {
				return nil, fmt.Errorf("entity[%d] table row line lookup: %w", index, err)
			}
			tableRowLines[lineIndex] = struct{}{}
			continue
		}
		if typ == otogi.TextEntityTypeImage {
			start := entity.Offset
			end := entity.Offset + entity.Length
			if start < 0 || end <= start || end > len(runes) {
				return nil, fmt.Errorf(
					"entity[%d] image has invalid range [%d,%d) for rune length %d",
					index,
					start,
					end,
					len(runes),
				)
			}
			ops = append(ops, telegramTextRewriteOp{
				start:       start,
				end:         end,
				replacement: formatTelegramReadableImage(entity.Image),
				reason:      "image_readable",
			})
		}
	}

	for lineIndex := range headingLines {
		line := lines[lineIndex]
		prefixLen := headingMarkerPrefixRuneLength(line.content)
		if prefixLen <= 0 {
			continue
		}
		ops = append(ops, telegramTextRewriteOp{
			start:       line.start,
			end:         line.start + prefixLen,
			replacement: "",
			reason:      "heading_marker",
		})
	}

	for lineIndex := range thematicLines {
		line := lines[lineIndex]
		if strings.TrimSpace(line.content) == telegramReadableThematicSeparator {
			continue
		}
		ops = append(ops, telegramTextRewriteOp{
			start:       line.start,
			end:         line.end,
			replacement: telegramReadableThematicSeparator,
			reason:      "thematic_separator",
		})
	}

	for lineIndex := range tableRowLines {
		line := lines[lineIndex]
		lineOps := buildTelegramTableRowOuterPipeRewriteOps(line)
		for _, op := range lineOps {
			ops = append(ops, op)
		}
	}

	for lineIndex, line := range lines {
		if !isMarkdownTableDelimiterLine(line.content) {
			continue
		}
		if !isLineBetweenTableRows(lineIndex, tableRowLines) {
			continue
		}
		end := line.end
		if end < len(runes) && runes[end] == '\n' {
			end++
		}
		ops = append(ops, telegramTextRewriteOp{
			start:       line.start,
			end:         end,
			replacement: "",
			reason:      "table_delimiter_remove",
		})
	}

	return ops, nil
}

func buildTelegramTrailingSpaceRewriteOps(text string) []telegramTextRewriteOp {
	runes := []rune(text)
	lines := indexTelegramTextLines(runes)
	ops := make([]telegramTextRewriteOp, 0)

	for _, line := range lines {
		trimEnd := line.end
		for trimEnd > line.start {
			value := runes[trimEnd-1]
			if value != ' ' && value != '\t' {
				break
			}
			trimEnd--
		}
		if trimEnd < line.end {
			ops = append(ops, telegramTextRewriteOp{
				start:       trimEnd,
				end:         line.end,
				replacement: "",
				reason:      "trim_trailing_spaces",
			})
		}
	}

	return ops
}

func buildTelegramEdgeNewlineTrimOps(text string) []telegramTextRewriteOp {
	runes := []rune(text)
	ops := make([]telegramTextRewriteOp, 0, 2)

	start := 0
	for start < len(runes) && runes[start] == '\n' {
		start++
	}
	if start > 0 {
		ops = append(ops, telegramTextRewriteOp{
			start:       0,
			end:         start,
			replacement: "",
			reason:      "trim_leading_newlines",
		})
	}

	end := len(runes)
	for end > start && runes[end-1] == '\n' {
		end--
	}
	if end < len(runes) {
		ops = append(ops, telegramTextRewriteOp{
			start:       end,
			end:         len(runes),
			replacement: "",
			reason:      "trim_trailing_newlines",
		})
	}

	return ops
}

func buildTelegramCollapseNewlineRewriteOps(text string) []telegramTextRewriteOp {
	runes := []rune(text)
	ops := make([]telegramTextRewriteOp, 0)

	for index := 0; index < len(runes); {
		if runes[index] != '\n' {
			index++
			continue
		}
		end := index + 1
		for end < len(runes) && runes[end] == '\n' {
			end++
		}
		if end-index > 2 {
			ops = append(ops, telegramTextRewriteOp{
				start:       index,
				end:         end,
				replacement: "\n\n",
				reason:      "collapse_newlines",
			})
		}
		index = end
	}

	return ops
}

func indexTelegramTextLines(runes []rune) []telegramTextLine {
	lines := make([]telegramTextLine, 0, strings.Count(string(runes), "\n")+1)
	lineStart := 0
	for index, value := range runes {
		if value != '\n' {
			continue
		}
		lines = append(lines, telegramTextLine{
			start:   lineStart,
			end:     index,
			content: string(runes[lineStart:index]),
		})
		lineStart = index + 1
	}
	lines = append(lines, telegramTextLine{
		start:   lineStart,
		end:     len(runes),
		content: string(runes[lineStart:]),
	})

	return lines
}

func telegramLineIndexForOffset(lines []telegramTextLine, offset int) (int, error) {
	if len(lines) == 0 {
		return 0, fmt.Errorf("no lines available")
	}
	if offset < 0 {
		return 0, fmt.Errorf("negative offset %d", offset)
	}

	for index, line := range lines {
		if offset < line.start {
			return index, nil
		}
		if offset <= line.end {
			return index, nil
		}
	}

	return len(lines) - 1, nil
}

func headingMarkerPrefixRuneLength(line string) int {
	runes := []rune(line)
	count := 0
	for count < len(runes) && runes[count] == '#' {
		count++
	}
	if count == 0 || count > 6 {
		return 0
	}
	if count >= len(runes) || runes[count] != ' ' {
		return 0
	}
	for count < len(runes) && runes[count] == ' ' {
		count++
	}
	return count
}

func formatTelegramReadableImage(meta *otogi.TextEntityImageMeta) string {
	if meta == nil {
		return "Image"
	}

	alt := strings.TrimSpace(meta.Alt)
	url := strings.TrimSpace(meta.URL)
	title := strings.TrimSpace(meta.Title)

	headline := "Image"
	if alt != "" {
		headline = "Image: " + alt
	}
	if url == "" {
		return headline
	}

	urlLine := "URL: " + url
	if title != "" {
		urlLine += " (title: " + title + ")"
	}
	return headline + "\n" + urlLine
}

func buildTelegramTableRowOuterPipeRewriteOps(line telegramTextLine) []telegramTextRewriteOp {
	lineRunes := []rune(line.content)
	if len(lineRunes) == 0 {
		return nil
	}

	leading := 0
	for leading < len(lineRunes) && (lineRunes[leading] == ' ' || lineRunes[leading] == '\t') {
		leading++
	}
	if leading >= len(lineRunes) || lineRunes[leading] != '|' {
		return nil
	}

	trailing := len(lineRunes) - 1
	for trailing >= 0 && (lineRunes[trailing] == ' ' || lineRunes[trailing] == '\t') {
		trailing--
	}
	if trailing <= leading || lineRunes[trailing] != '|' {
		return nil
	}

	leadEnd := leading + 1
	if leadEnd < len(lineRunes) && lineRunes[leadEnd] == ' ' {
		leadEnd++
	}

	tailStart := trailing
	if tailStart-1 > leadEnd && lineRunes[tailStart-1] == ' ' {
		tailStart--
	}
	if tailStart <= leadEnd {
		return nil
	}

	return []telegramTextRewriteOp{
		{
			start:       line.start + leading,
			end:         line.start + leadEnd,
			replacement: "",
			reason:      "table_row_leading_pipe",
		},
		{
			start:       line.start + tailStart,
			end:         line.start + trailing + 1,
			replacement: "",
			reason:      "table_row_trailing_pipe",
		},
	}
}

func isMarkdownTableDelimiterLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}

	if strings.HasPrefix(trimmed, "|") {
		trimmed = strings.TrimPrefix(trimmed, "|")
	}
	if strings.HasSuffix(trimmed, "|") {
		trimmed = strings.TrimSuffix(trimmed, "|")
	}

	cells := strings.Split(trimmed, "|")
	if len(cells) == 0 {
		return false
	}
	for _, cell := range cells {
		if !isMarkdownTableDelimiterCell(strings.TrimSpace(cell)) {
			return false
		}
	}
	return true
}

func isMarkdownTableDelimiterCell(cell string) bool {
	if len(cell) < 3 {
		return false
	}
	if strings.Count(cell, "-") < 3 {
		return false
	}
	for _, value := range cell {
		if value != '-' && value != ':' {
			return false
		}
	}

	colonCount := strings.Count(cell, ":")
	if colonCount > 2 {
		return false
	}
	if strings.Contains(cell[1:len(cell)-1], ":") {
		return false
	}
	switch colonCount {
	case 0:
		return true
	case 1:
		return cell[0] == ':' || cell[len(cell)-1] == ':'
	case 2:
		return cell[0] == ':' && cell[len(cell)-1] == ':'
	default:
		return false
	}
}

func isLineBetweenTableRows(index int, tableRowLines map[int]struct{}) bool {
	if index <= 0 {
		return false
	}
	if _, hasPrev := tableRowLines[index-1]; !hasPrev {
		return false
	}
	if _, hasNext := tableRowLines[index+1]; !hasNext {
		return false
	}
	return true
}

package kernel

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"ex-otogi/pkg/otogi"

	"github.com/yuin/goldmark"
	gast "github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

type markdownParser struct {
	markdown goldmark.Markdown
}

func newMarkdownParser() otogi.MarkdownParser {
	return &markdownParser{
		markdown: goldmark.New(
			goldmark.WithExtensions(extension.GFM),
		),
	}
}

func (p *markdownParser) ParseMarkdown(ctx context.Context, markdown string) (parsed otogi.ParsedText, err error) {
	if ctx == nil {
		return otogi.ParsedText{}, fmt.Errorf("parse markdown: nil context")
	}
	if err := ctx.Err(); err != nil {
		return otogi.ParsedText{}, fmt.Errorf("parse markdown context: %w", err)
	}

	defer func() {
		if recovered := recover(); recovered != nil {
			panicErr := fmt.Errorf("%v", recovered)
			if recoveredErr, ok := recovered.(error); ok {
				panicErr = recoveredErr
			}
			err = fmt.Errorf("parse markdown panic: %w", panicErr)
		}
	}()

	source := normalizeMarkdownInput(markdown)
	document := p.markdown.Parser().Parse(text.NewReader([]byte(source)))

	state := markdownRenderState{
		source: []byte(source),
	}
	if err := state.renderDocument(ctx, document); err != nil {
		return otogi.ParsedText{}, fmt.Errorf("parse markdown render: %w", err)
	}

	parsed = otogi.ParsedText{
		Text:     state.text(),
		Entities: state.entities(),
	}
	if err := otogi.ValidateTextEntities(parsed.Text, parsed.Entities); err != nil {
		return otogi.ParsedText{}, fmt.Errorf("parse markdown validate entities: %w", err)
	}

	return parsed, nil
}

func normalizeMarkdownInput(raw string) string {
	normalized := strings.ReplaceAll(raw, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	return normalized
}

type markdownRenderState struct {
	source []byte

	buffer         []rune
	entitiesBuffer []otogi.TextEntity

	tableCount int
}

func (s *markdownRenderState) text() string {
	return string(s.buffer)
}

func (s *markdownRenderState) entities() []otogi.TextEntity {
	if len(s.entitiesBuffer) == 0 {
		return nil
	}

	cloned := make([]otogi.TextEntity, len(s.entitiesBuffer))
	copy(cloned, s.entitiesBuffer)
	return cloned
}

func (s *markdownRenderState) offset() int {
	return len(s.buffer)
}

func (s *markdownRenderState) appendString(raw string) {
	for _, value := range raw {
		s.buffer = append(s.buffer, value)
	}
}

func (s *markdownRenderState) appendSpace() {
	if len(s.buffer) == 0 {
		return
	}
	last := s.buffer[len(s.buffer)-1]
	if last == ' ' || last == '\n' {
		return
	}
	s.buffer = append(s.buffer, ' ')
}

func (s *markdownRenderState) appendNewline() {
	if len(s.buffer) == 0 {
		return
	}
	if s.buffer[len(s.buffer)-1] == '\n' {
		return
	}
	s.buffer = append(s.buffer, '\n')
}

func (s *markdownRenderState) trimTrailingSpaces(minOffset int) {
	for len(s.buffer) > minOffset {
		last := s.buffer[len(s.buffer)-1]
		if last != ' ' && last != '\t' {
			return
		}
		s.buffer = s.buffer[:len(s.buffer)-1]
	}
}

func (s *markdownRenderState) addEntity(start int, entity otogi.TextEntity) {
	length := s.offset() - start
	if length <= 0 {
		return
	}
	entity.Offset = start
	entity.Length = length
	s.entitiesBuffer = append(s.entitiesBuffer, entity)
}

func (s *markdownRenderState) renderDocument(ctx context.Context, document gast.Node) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("render document context: %w", err)
	}

	for block := document.FirstChild(); block != nil; block = block.NextSibling() {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("render document context: %w", err)
		}

		beforeBlock := s.offset()
		separatorAdded := false
		if beforeBlock > 0 {
			s.appendNewline()
			separatorAdded = s.offset() > beforeBlock
		}

		if err := s.renderBlock(ctx, block); err != nil {
			return err
		}
		if separatorAdded && s.offset() == beforeBlock+1 {
			s.buffer = s.buffer[:beforeBlock]
		}
	}

	return nil
}

func (s *markdownRenderState) renderBlock(ctx context.Context, block gast.Node) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("render block context: %w", err)
	}

	switch node := block.(type) {
	case *gast.Paragraph, *gast.TextBlock:
		return s.renderInlineChildren(ctx, node)
	case *gast.Heading:
		return s.renderHeading(ctx, node)
	case *gast.ThematicBreak:
		start := s.offset()
		s.appendString("---")
		s.addEntity(start, otogi.TextEntity{Type: otogi.TextEntityTypeThematicBreak})
		return nil
	case *gast.FencedCodeBlock:
		return s.renderFencedCodeBlock(node)
	case *gast.CodeBlock:
		return s.renderCodeBlock(node)
	case *gast.Blockquote:
		return s.renderBlockquote(ctx, node)
	case *gast.List:
		return s.renderList(ctx, node, 1)
	case *extast.Table:
		return s.renderTable(ctx, node)
	case *gast.HTMLBlock:
		return s.renderBlockLines(node.Lines())
	default:
		if block.FirstChild() != nil {
			for child := block.FirstChild(); child != nil; child = child.NextSibling() {
				if err := ctx.Err(); err != nil {
					return fmt.Errorf("render block context: %w", err)
				}

				before := s.offset()
				separatorAdded := false
				if before > 0 {
					s.appendNewline()
					separatorAdded = s.offset() > before
				}
				if err := s.renderBlock(ctx, child); err != nil {
					return err
				}
				if separatorAdded && s.offset() == before+1 {
					s.buffer = s.buffer[:before]
				}
			}
			return nil
		}
		return s.renderBlockLines(block.Lines())
	}
}

func (s *markdownRenderState) renderHeading(ctx context.Context, heading *gast.Heading) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("render heading context: %w", err)
	}

	level := heading.Level
	if level < 1 {
		level = 1
	}
	if level > 6 {
		level = 6
	}

	start := s.offset()
	s.appendString(strings.Repeat("#", level))
	if heading.FirstChild() != nil {
		s.appendString(" ")
	}
	if err := s.renderInlineChildren(ctx, heading); err != nil {
		return err
	}
	s.trimTrailingSpaces(start)
	s.addEntity(start, otogi.TextEntity{
		Type: otogi.TextEntityTypeHeading,
		Heading: &otogi.TextEntityHeadingMeta{
			Level: level,
		},
	})

	return nil
}

func (s *markdownRenderState) renderFencedCodeBlock(block *gast.FencedCodeBlock) error {
	start := s.offset()
	if err := s.renderBlockLines(block.Lines()); err != nil {
		return err
	}

	language := strings.TrimSpace(string(block.Language(s.source)))
	s.addEntity(start, otogi.TextEntity{
		Type:     otogi.TextEntityTypePre,
		Language: language,
	})

	return nil
}

func (s *markdownRenderState) renderCodeBlock(block *gast.CodeBlock) error {
	start := s.offset()
	if err := s.renderBlockLines(block.Lines()); err != nil {
		return err
	}

	s.addEntity(start, otogi.TextEntity{
		Type: otogi.TextEntityTypePre,
	})

	return nil
}

func (s *markdownRenderState) renderBlockquote(ctx context.Context, blockquote *gast.Blockquote) error {
	start := s.offset()
	for child := blockquote.FirstChild(); child != nil; child = child.NextSibling() {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("render blockquote context: %w", err)
		}
		if child != blockquote.FirstChild() {
			s.appendNewline()
		}
		s.appendString("> ")
		switch child.(type) {
		case *gast.Paragraph, *gast.TextBlock:
			if err := s.renderInlineChildren(ctx, child); err != nil {
				return err
			}
		default:
			if err := s.renderBlock(ctx, child); err != nil {
				return err
			}
		}
	}
	s.trimTrailingSpaces(start)

	s.addEntity(start, otogi.TextEntity{
		Type: otogi.TextEntityTypeBlockquote,
	})
	return nil
}

func (s *markdownRenderState) renderList(ctx context.Context, list *gast.List, depth int) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("render list context: %w", err)
	}

	start := s.offset()
	for index, itemNode := 0, list.FirstChild(); itemNode != nil; index, itemNode = index+1, itemNode.NextSibling() {
		item, ok := itemNode.(*gast.ListItem)
		if !ok {
			continue
		}
		if index > 0 {
			s.appendNewline()
		}
		if err := s.renderListItemLine(ctx, item, list, depth, index); err != nil {
			return err
		}

		for child := item.FirstChild(); child != nil; child = child.NextSibling() {
			nested, ok := child.(*gast.List)
			if !ok {
				continue
			}
			s.appendNewline()
			if err := s.renderList(ctx, nested, depth+1); err != nil {
				return err
			}
		}
	}

	s.addEntity(start, otogi.TextEntity{
		Type: otogi.TextEntityTypeList,
		List: &otogi.TextEntityListMeta{
			Depth:   depth,
			Ordered: list.IsOrdered(),
		},
	})
	return nil
}

func (s *markdownRenderState) renderListItemLine(
	ctx context.Context,
	item *gast.ListItem,
	list *gast.List,
	depth int,
	index int,
) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("render list item context: %w", err)
	}

	start := s.offset()
	s.appendString(strings.Repeat("  ", maxInt(depth-1, 0)))

	itemNumber := 0
	if list.IsOrdered() {
		itemNumber = list.Start + index
		if itemNumber <= 0 {
			itemNumber = index + 1
		}
		s.appendString(strconv.Itoa(itemNumber))
		s.appendString(". ")
	} else {
		s.appendString("- ")
	}

	content := firstNonListChild(item)
	checked, hasTask := taskCheckbox(content)
	if content != nil {
		switch content.(type) {
		case *gast.Paragraph, *gast.TextBlock, *gast.Heading:
			if err := s.renderInlineChildren(ctx, content); err != nil {
				return err
			}
		default:
			if err := s.renderBlock(ctx, content); err != nil {
				return err
			}
		}
	}
	s.trimTrailingSpaces(start)

	entity := otogi.TextEntity{
		Type: otogi.TextEntityTypeListItem,
		List: &otogi.TextEntityListMeta{
			Depth:      depth,
			Ordered:    list.IsOrdered(),
			ItemNumber: itemNumber,
		},
	}
	s.addEntity(start, entity)
	if hasTask {
		s.addEntity(start, otogi.TextEntity{
			Type: otogi.TextEntityTypeTaskItem,
			List: &otogi.TextEntityListMeta{
				Depth:      depth,
				Ordered:    list.IsOrdered(),
				ItemNumber: itemNumber,
			},
			Task: &otogi.TextEntityTaskMeta{
				Checked: checked,
			},
		})
	}

	return nil
}

func firstNonListChild(item *gast.ListItem) gast.Node {
	for child := item.FirstChild(); child != nil; child = child.NextSibling() {
		if _, isNestedList := child.(*gast.List); isNestedList {
			continue
		}
		return child
	}
	return nil
}

func taskCheckbox(node gast.Node) (checked bool, exists bool) {
	if node == nil {
		return false, false
	}
	checkbox, ok := node.FirstChild().(*extast.TaskCheckBox)
	if !ok {
		return false, false
	}
	return checkbox.IsChecked, true
}

func (s *markdownRenderState) nextTableGroupID() string {
	s.tableCount++
	return fmt.Sprintf("table:%d", s.tableCount)
}

func (s *markdownRenderState) renderTable(ctx context.Context, table *extast.Table) error {
	groupID := s.nextTableGroupID()
	start := s.offset()

	rowIndex := 0
	for child := table.FirstChild(); child != nil; child = child.NextSibling() {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("render table context: %w", err)
		}

		switch row := child.(type) {
		case *extast.TableHeader:
			if err := s.renderTableRow(ctx, row, rowIndex, true, groupID); err != nil {
				return err
			}
			rowIndex++
			s.appendNewline()
			s.renderTableDelimiter(table.Alignments)
			if child.NextSibling() != nil {
				s.appendNewline()
			}
		case *extast.TableRow:
			if err := s.renderTableRow(ctx, row, rowIndex, false, groupID); err != nil {
				return err
			}
			rowIndex++
			if child.NextSibling() != nil {
				s.appendNewline()
			}
		}
	}

	s.addEntity(start, otogi.TextEntity{
		Type: otogi.TextEntityTypeTable,
		Table: &otogi.TextEntityTableMeta{
			GroupID: groupID,
		},
	})

	return nil
}

func (s *markdownRenderState) renderTableRow(
	ctx context.Context,
	rowNode gast.Node,
	rowIndex int,
	isHeader bool,
	groupID string,
) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("render table row context: %w", err)
	}

	start := s.offset()
	s.appendString("|")
	column := 0
	for cellNode := rowNode.FirstChild(); cellNode != nil; cellNode = cellNode.NextSibling() {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("render table row context: %w", err)
		}

		cell, ok := cellNode.(*extast.TableCell)
		if !ok {
			continue
		}
		s.appendString(" ")
		cellStart := s.offset()
		if err := s.renderInlineChildren(ctx, cell); err != nil {
			return err
		}
		s.addEntity(cellStart, otogi.TextEntity{
			Type: otogi.TextEntityTypeTableCell,
			Table: &otogi.TextEntityTableMeta{
				GroupID:   groupID,
				Row:       rowIndex,
				Column:    column,
				Header:    isHeader,
				Alignment: tableAlignmentString(cell.Alignment),
			},
		})
		s.appendString(" |")
		column++
	}

	s.addEntity(start, otogi.TextEntity{
		Type: otogi.TextEntityTypeTableRow,
		Table: &otogi.TextEntityTableMeta{
			GroupID: groupID,
			Row:     rowIndex,
			Header:  isHeader,
		},
	})
	return nil
}

func (s *markdownRenderState) renderTableDelimiter(alignments []extast.Alignment) {
	s.appendString("|")
	if len(alignments) == 0 {
		s.appendString(" --- |")
		return
	}
	for _, alignment := range alignments {
		s.appendString(" ")
		s.appendString(tableDelimiter(alignment))
		s.appendString(" |")
	}
}

func tableDelimiter(alignment extast.Alignment) string {
	switch alignment {
	case extast.AlignLeft:
		return ":---"
	case extast.AlignCenter:
		return ":---:"
	case extast.AlignRight:
		return "---:"
	default:
		return "---"
	}
}

func tableAlignmentString(alignment extast.Alignment) string {
	switch alignment {
	case extast.AlignLeft:
		return "left"
	case extast.AlignCenter:
		return "center"
	case extast.AlignRight:
		return "right"
	default:
		return "none"
	}
}

func (s *markdownRenderState) renderBlockLines(lines *text.Segments) error {
	if lines == nil || lines.Len() == 0 {
		return nil
	}
	for index := 0; index < lines.Len(); index++ {
		segment := lines.At(index)
		line := string(segment.Value(s.source))
		line = strings.TrimSuffix(line, "\n")
		line = strings.TrimSuffix(line, "\r")
		line = strings.TrimRight(line, " \t")
		if index > 0 {
			s.appendNewline()
		}
		s.appendString(line)
	}
	return nil
}

func (s *markdownRenderState) renderInlineChildren(ctx context.Context, parent gast.Node) error {
	for inline := parent.FirstChild(); inline != nil; inline = inline.NextSibling() {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("render inline context: %w", err)
		}
		if err := s.renderInlineNode(ctx, inline); err != nil {
			return err
		}
	}
	return nil
}

func (s *markdownRenderState) renderInlineNode(ctx context.Context, node gast.Node) error {
	switch inline := node.(type) {
	case *gast.Text:
		value := string(inline.Value(s.source))
		if inline.SoftLineBreak() || inline.HardLineBreak() {
			value = strings.TrimRight(value, " \t")
		}
		s.appendString(value)
		if inline.HardLineBreak() {
			s.appendNewline()
		}
		if inline.SoftLineBreak() {
			s.appendSpace()
		}
		return nil
	case *gast.String:
		s.appendString(string(inline.Value))
		return nil
	case *gast.Emphasis:
		start := s.offset()
		if err := s.renderInlineChildren(ctx, inline); err != nil {
			return err
		}
		entityType := otogi.TextEntityTypeItalic
		if inline.Level >= 2 {
			entityType = otogi.TextEntityTypeBold
		}
		s.addEntity(start, otogi.TextEntity{Type: entityType})
		return nil
	case *extast.Strikethrough:
		start := s.offset()
		if err := s.renderInlineChildren(ctx, inline); err != nil {
			return err
		}
		s.addEntity(start, otogi.TextEntity{Type: otogi.TextEntityTypeStrike})
		return nil
	case *gast.CodeSpan:
		start := s.offset()
		if err := s.renderInlineChildren(ctx, inline); err != nil {
			return err
		}
		s.addEntity(start, otogi.TextEntity{Type: otogi.TextEntityTypeCode})
		return nil
	case *gast.Link:
		start := s.offset()
		if err := s.renderInlineChildren(ctx, inline); err != nil {
			return err
		}
		s.addEntity(start, otogi.TextEntity{
			Type: otogi.TextEntityTypeTextURL,
			URL:  strings.TrimSpace(string(inline.Destination)),
		})
		return nil
	case *gast.AutoLink:
		start := s.offset()
		label := string(inline.Label(s.source))
		s.appendString(label)
		entityType := otogi.TextEntityTypeURL
		if inline.AutoLinkType == gast.AutoLinkEmail {
			entityType = otogi.TextEntityTypeEmail
		}
		s.addEntity(start, otogi.TextEntity{Type: entityType})
		return nil
	case *gast.Image:
		alt := strings.TrimSpace(s.collectInlineText(inline))
		imageURL := strings.TrimSpace(string(inline.Destination))
		imageTitle := strings.TrimSpace(string(inline.Title))

		start := s.offset()
		s.appendString(formatMarkdownImage(alt, imageURL, imageTitle))
		s.addEntity(start, otogi.TextEntity{
			Type: otogi.TextEntityTypeImage,
			Image: &otogi.TextEntityImageMeta{
				URL:   imageURL,
				Title: imageTitle,
				Alt:   alt,
			},
		})
		return nil
	case *extast.TaskCheckBox:
		if inline.IsChecked {
			s.appendString("[x] ")
		} else {
			s.appendString("[ ] ")
		}
		return nil
	case *gast.RawHTML:
		s.appendString(rawHTMLText(inline, s.source))
		return nil
	default:
		if node.FirstChild() != nil {
			return s.renderInlineChildren(ctx, node)
		}
		return nil
	}
}

func formatMarkdownImage(alt, url, title string) string {
	if title == "" {
		return "![" + alt + "](" + url + ")"
	}
	return "![" + alt + "](" + url + " \"" + title + "\")"
}

func rawHTMLText(node *gast.RawHTML, source []byte) string {
	if node == nil || node.Segments == nil {
		return ""
	}
	builder := strings.Builder{}
	for index := 0; index < node.Segments.Len(); index++ {
		segment := node.Segments.At(index)
		builder.Write(segment.Value(source))
	}
	return builder.String()
}

func (s *markdownRenderState) collectInlineText(node gast.Node) string {
	if node == nil {
		return ""
	}

	builder := strings.Builder{}
	var walk func(gast.Node)
	walk = func(current gast.Node) {
		switch typed := current.(type) {
		case *gast.Text:
			builder.Write(typed.Value(s.source))
			if typed.SoftLineBreak() || typed.HardLineBreak() {
				builder.WriteString(" ")
			}
		case *gast.String:
			builder.Write(typed.Value)
		case *gast.AutoLink:
			builder.Write(typed.Label(s.source))
		default:
			for child := current.FirstChild(); child != nil; child = child.NextSibling() {
				walk(child)
			}
		}
	}

	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		walk(child)
	}

	return strings.Join(strings.Fields(builder.String()), " ")
}

func maxInt(left int, right int) int {
	if left > right {
		return left
	}
	return right
}

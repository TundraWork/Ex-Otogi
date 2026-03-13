package telegram

import (
	"strings"
	"testing"

	"ex-otogi/pkg/otogi/platform"
)

func TestRenderTelegramReadableTextHeadingThematicAndSpacing(t *testing.T) {
	t.Parallel()

	text := "# 😀bold\n\n\n---\n"
	thematicOffset := runeIndex(text, "---")
	rendered, err := renderTelegramReadableText(text, []platform.TextEntity{
		{
			Type:   platform.TextEntityTypeHeading,
			Offset: 0,
			Length: runeLen("# 😀bold"),
			Heading: &platform.TextEntityHeadingMeta{
				Level: 1,
			},
		},
		{
			Type:   platform.TextEntityTypeBold,
			Offset: runeLen("# "),
			Length: runeLen("😀bold"),
		},
		{
			Type:   platform.TextEntityTypeThematicBreak,
			Offset: thematicOffset,
			Length: runeLen("---"),
		},
	})
	if err != nil {
		t.Fatalf("renderTelegramReadableText failed: %v", err)
	}

	wantText := "😀bold\n\n----------------"
	if rendered.renderedText != wantText {
		t.Fatalf("rendered text = %q, want %q", rendered.renderedText, wantText)
	}

	bold, ok := findEntityByType(rendered.renderedEntities, platform.TextEntityTypeBold)
	if !ok {
		t.Fatal("bold entity not found")
	}
	if bold.Offset != 0 || bold.Length != runeLen("😀bold") {
		t.Fatalf("bold range = [%d,%d), want [0,%d)", bold.Offset, bold.Offset+bold.Length, runeLen("😀bold"))
	}
}

func TestRenderTelegramReadableTextTableCleanupAndRemap(t *testing.T) {
	t.Parallel()

	text := "| h1 | h2 |\n| --- | :---: |\n| x | y |"
	row0Offset := 0
	row2Offset := runeIndex(text, "| x | y |")
	boldOffset := runeIndex(text, "x")

	rendered, err := renderTelegramReadableText(text, []platform.TextEntity{
		{
			Type:   platform.TextEntityTypeTable,
			Offset: 0,
			Length: runeLen(text),
			Table: &platform.TextEntityTableMeta{
				GroupID: "table:1",
			},
		},
		{
			Type:   platform.TextEntityTypeTableRow,
			Offset: row0Offset,
			Length: runeLen("| h1 | h2 |"),
			Table: &platform.TextEntityTableMeta{
				GroupID: "table:1",
				Row:     0,
				Header:  true,
			},
		},
		{
			Type:   platform.TextEntityTypeTableRow,
			Offset: row2Offset,
			Length: runeLen("| x | y |"),
			Table: &platform.TextEntityTableMeta{
				GroupID: "table:1",
				Row:     1,
				Header:  false,
			},
		},
		{
			Type:   platform.TextEntityTypeBold,
			Offset: boldOffset,
			Length: runeLen("x"),
		},
	})
	if err != nil {
		t.Fatalf("renderTelegramReadableText failed: %v", err)
	}

	wantText := "h1 | h2\nx | y"
	if rendered.renderedText != wantText {
		t.Fatalf("rendered text = %q, want %q", rendered.renderedText, wantText)
	}

	bold, ok := findEntityByType(rendered.renderedEntities, platform.TextEntityTypeBold)
	if !ok {
		t.Fatal("bold entity not found")
	}
	if bold.Offset != runeLen("h1 | h2\n") || bold.Length != 1 {
		t.Fatalf(
			"bold range = [%d,%d), want [%d,%d)",
			bold.Offset,
			bold.Offset+bold.Length,
			runeLen("h1 | h2\n"),
			runeLen("h1 | h2\n")+1,
		)
	}
}

func TestRenderTelegramReadableTextImageRewrite(t *testing.T) {
	t.Parallel()

	text := "![logo](https://img \"brand\")"
	rendered, err := renderTelegramReadableText(text, []platform.TextEntity{
		{
			Type:   platform.TextEntityTypeImage,
			Offset: 0,
			Length: runeLen(text),
			Image: &platform.TextEntityImageMeta{
				URL:   "https://img",
				Title: "brand",
				Alt:   "logo",
			},
		},
	})
	if err != nil {
		t.Fatalf("renderTelegramReadableText failed: %v", err)
	}

	wantText := "Image: logo\nURL: https://img (title: brand)"
	if rendered.renderedText != wantText {
		t.Fatalf("rendered text = %q, want %q", rendered.renderedText, wantText)
	}
}

func TestRenderTelegramReadableTextNormalizesWhitespace(t *testing.T) {
	t.Parallel()

	text := "\n\nLine 1   \r\n\r\n\r\nLine 2\t \r\n\n"
	rendered, err := renderTelegramReadableText(text, nil)
	if err != nil {
		t.Fatalf("renderTelegramReadableText failed: %v", err)
	}

	wantText := "Line 1\n\nLine 2"
	if rendered.renderedText != wantText {
		t.Fatalf("rendered text = %q, want %q", rendered.renderedText, wantText)
	}
}

func TestRenderTelegramReadableTextMultibyteOffsetRemap(t *testing.T) {
	t.Parallel()

	text := "# 😀x"
	rendered, err := renderTelegramReadableText(text, []platform.TextEntity{
		{
			Type:   platform.TextEntityTypeHeading,
			Offset: 0,
			Length: runeLen(text),
			Heading: &platform.TextEntityHeadingMeta{
				Level: 1,
			},
		},
		{
			Type:   platform.TextEntityTypeItalic,
			Offset: runeLen("# "),
			Length: runeLen("😀x"),
		},
	})
	if err != nil {
		t.Fatalf("renderTelegramReadableText failed: %v", err)
	}

	if rendered.renderedText != "😀x" {
		t.Fatalf("rendered text = %q, want %q", rendered.renderedText, "😀x")
	}

	italic, ok := findEntityByType(rendered.renderedEntities, platform.TextEntityTypeItalic)
	if !ok {
		t.Fatal("italic entity not found")
	}
	if italic.Offset != 0 || italic.Length != runeLen("😀x") {
		t.Fatalf("italic range = [%d,%d), want [0,%d)", italic.Offset, italic.Offset+italic.Length, runeLen("😀x"))
	}
}

func TestApplyTelegramRewriteOpsRejectsOverlap(t *testing.T) {
	t.Parallel()

	_, _, err := applyTelegramRewriteOps("abcdef", []telegramTextRewriteOp{
		{start: 1, end: 4, replacement: "X"},
		{start: 3, end: 5, replacement: "Y"},
	})
	if err == nil {
		t.Fatal("expected overlap error")
	}
}

func TestApplyTelegramRewriteOpsSortsDeterministically(t *testing.T) {
	t.Parallel()

	got, _, err := applyTelegramRewriteOps("abcdefg", []telegramTextRewriteOp{
		{start: 4, end: 6, replacement: "X"},
		{start: 1, end: 3, replacement: "Y"},
	})
	if err != nil {
		t.Fatalf("applyTelegramRewriteOps failed: %v", err)
	}
	if got != "aYdXg" {
		t.Fatalf("rewritten text = %q, want %q", got, "aYdXg")
	}
}

func findEntityByType(entities []platform.TextEntity, typ platform.TextEntityType) (platform.TextEntity, bool) {
	for _, entity := range entities {
		if entity.Type == typ {
			return entity, true
		}
	}
	return platform.TextEntity{}, false
}

func runeIndex(text string, token string) int {
	byteOffset := strings.Index(text, token)
	if byteOffset < 0 {
		return -1
	}
	return runeLen(text[:byteOffset])
}

func runeLen(text string) int {
	return len([]rune(text))
}

package kernel

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"ex-otogi/pkg/otogi/core"
	"ex-otogi/pkg/otogi/platform"
)

func TestMarkdownParserParseMarkdownBasicMappings(t *testing.T) {
	t.Parallel()

	parser := newMarkdownParser()
	got, err := parser.ParseMarkdown(
		context.Background(),
		"**bold** _italic_ ~~strike~~ `code` [link](https://example.com) <https://otogi.dev> <a@b.dev>",
	)
	if err != nil {
		t.Fatalf("ParseMarkdown failed: %v", err)
	}
	if got.Text != "bold italic strike code link https://otogi.dev a@b.dev" {
		t.Fatalf("text = %q", got.Text)
	}

	assertEntityTypeCount(t, got.Entities, platform.TextEntityTypeBold, 1)
	assertEntityTypeCount(t, got.Entities, platform.TextEntityTypeItalic, 1)
	assertEntityTypeCount(t, got.Entities, platform.TextEntityTypeStrike, 1)
	assertEntityTypeCount(t, got.Entities, platform.TextEntityTypeCode, 1)
	assertEntityTypeCount(t, got.Entities, platform.TextEntityTypeTextURL, 1)
	assertEntityTypeCount(t, got.Entities, platform.TextEntityTypeURL, 1)
	assertEntityTypeCount(t, got.Entities, platform.TextEntityTypeEmail, 1)
}

func TestMarkdownParserParseMarkdownStructuralMappings(t *testing.T) {
	t.Parallel()

	parser := newMarkdownParser()
	got, err := parser.ParseMarkdown(
		context.Background(),
		"# Head\n\n- [x] done\n- item\n\n---\n\n| a | b |\n|---|:---:|\n| c | d |\n\n![alt](https://img \"title\")",
	)
	if err != nil {
		t.Fatalf("ParseMarkdown failed: %v", err)
	}

	wantText := "# Head\n- [x] done\n- item\n---\n| a | b |\n| --- | :---: |\n| c | d |\n![alt](https://img \"title\")"
	if got.Text != wantText {
		t.Fatalf("text = %q, want %q", got.Text, wantText)
	}

	assertEntityTypeCount(t, got.Entities, platform.TextEntityTypeHeading, 1)
	assertEntityTypeCount(t, got.Entities, platform.TextEntityTypeList, 1)
	assertEntityTypeCount(t, got.Entities, platform.TextEntityTypeListItem, 2)
	assertEntityTypeCount(t, got.Entities, platform.TextEntityTypeTaskItem, 1)
	assertEntityTypeCount(t, got.Entities, platform.TextEntityTypeThematicBreak, 1)
	assertEntityTypeCount(t, got.Entities, platform.TextEntityTypeTable, 1)
	assertEntityTypeCount(t, got.Entities, platform.TextEntityTypeTableRow, 2)
	assertEntityTypeCount(t, got.Entities, platform.TextEntityTypeTableCell, 4)
	assertEntityTypeCount(t, got.Entities, platform.TextEntityTypeImage, 1)

	var tableGroupID string
	rowIndexes := map[int]bool{}
	headerCellCount := 0
	bodyCellCount := 0
	for _, entity := range got.Entities {
		if entity.Type != platform.TextEntityTypeTable &&
			entity.Type != platform.TextEntityTypeTableRow &&
			entity.Type != platform.TextEntityTypeTableCell {
			continue
		}
		if entity.Table == nil {
			t.Fatalf("%s metadata is nil", entity.Type)
		}
		if entity.Table.GroupID == "" {
			t.Fatalf("%s group id is empty", entity.Type)
		}
		if tableGroupID == "" {
			tableGroupID = entity.Table.GroupID
		}
		if entity.Table.GroupID != tableGroupID {
			t.Fatalf("group id = %q, want %q", entity.Table.GroupID, tableGroupID)
		}
		if entity.Type == platform.TextEntityTypeTableRow {
			rowIndexes[entity.Table.Row] = true
		}
		if entity.Type == platform.TextEntityTypeTableCell {
			if entity.Table.Row < 0 {
				t.Fatalf("table cell row = %d, want >= 0", entity.Table.Row)
			}
			if entity.Table.Column < 0 {
				t.Fatalf("table cell column = %d, want >= 0", entity.Table.Column)
			}
			if entity.Table.Header {
				headerCellCount++
				if entity.Table.Row != 0 {
					t.Fatalf("header cell row = %d, want 0", entity.Table.Row)
				}
			} else {
				bodyCellCount++
			}
		}
	}
	if tableGroupID != "table:1" {
		t.Fatalf("table group id = %q, want table:1", tableGroupID)
	}
	if !rowIndexes[0] || !rowIndexes[1] || len(rowIndexes) != 2 {
		t.Fatalf("row indexes = %#v, want {0,1}", rowIndexes)
	}
	if headerCellCount != 2 {
		t.Fatalf("header cell count = %d, want 2", headerCellCount)
	}
	if bodyCellCount != 2 {
		t.Fatalf("body cell count = %d, want 2", bodyCellCount)
	}
}

func TestMarkdownParserParseMarkdownTableGroupIDDeterministic(t *testing.T) {
	t.Parallel()

	parser := newMarkdownParser()
	got, err := parser.ParseMarkdown(
		context.Background(),
		"| a |\n| --- |\n| b |\n\n| c |\n| --- |\n| d |",
	)
	if err != nil {
		t.Fatalf("ParseMarkdown failed: %v", err)
	}

	groupIDs := make([]string, 0, 2)
	for _, entity := range got.Entities {
		if entity.Type != platform.TextEntityTypeTable {
			continue
		}
		if entity.Table == nil {
			t.Fatal("table entity metadata is nil")
		}
		groupIDs = append(groupIDs, entity.Table.GroupID)
	}
	if !reflect.DeepEqual(groupIDs, []string{"table:1", "table:2"}) {
		t.Fatalf("table group ids = %#v, want [table:1 table:2]", groupIDs)
	}
}

func TestMarkdownParserParseMarkdownRuneOffsets(t *testing.T) {
	t.Parallel()

	parser := newMarkdownParser()
	got, err := parser.ParseMarkdown(context.Background(), "**😀x**")
	if err != nil {
		t.Fatalf("ParseMarkdown failed: %v", err)
	}
	if got.Text != "😀x" {
		t.Fatalf("text = %q, want %q", got.Text, "😀x")
	}
	if len(got.Entities) != 1 {
		t.Fatalf("entities len = %d, want 1", len(got.Entities))
	}
	if got.Entities[0].Type != platform.TextEntityTypeBold {
		t.Fatalf("entity type = %q, want %q", got.Entities[0].Type, platform.TextEntityTypeBold)
	}
	if got.Entities[0].Offset != 0 || got.Entities[0].Length != 2 {
		t.Fatalf("entity range = [%d,%d)", got.Entities[0].Offset, got.Entities[0].Offset+got.Entities[0].Length)
	}
}

func TestMarkdownParserParseMarkdownContextCanceled(t *testing.T) {
	t.Parallel()

	parser := newMarkdownParser()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := parser.ParseMarkdown(ctx, "test"); err == nil {
		t.Fatal("expected context cancellation error")
	}
}

func TestMarkdownParserParseMarkdownRecoversPanic(t *testing.T) {
	t.Parallel()

	parser := &markdownParser{}
	if _, err := parser.ParseMarkdown(context.Background(), "test"); err == nil {
		t.Fatal("expected panic recovery error")
	} else if !strings.Contains(err.Error(), "parse markdown panic") {
		t.Fatalf("error = %q, want parse markdown panic", err.Error())
	}
}

func TestMarkdownParserParseMarkdownBestEffortHTMLBlock(t *testing.T) {
	t.Parallel()

	parser := newMarkdownParser()
	got, err := parser.ParseMarkdown(context.Background(), "<details>\nhello\n</details>")
	if err != nil {
		t.Fatalf("ParseMarkdown failed: %v", err)
	}
	if got.Text == "" {
		t.Fatal("text is empty, want best-effort output")
	}
}

func TestKernelProvidesMarkdownParserService(t *testing.T) {
	t.Parallel()

	kernelRuntime := newTestKernel(t)
	parser, err := core.ResolveAs[platform.MarkdownParser](
		kernelRuntime.Services(),
		platform.ServiceMarkdownParser,
	)
	if err != nil {
		t.Fatalf("resolve markdown parser failed: %v", err)
	}
	if parser == nil {
		t.Fatal("resolved markdown parser is nil")
	}
}

func assertEntityTypeCount(
	t *testing.T,
	entities []platform.TextEntity,
	typ platform.TextEntityType,
	want int,
) {
	t.Helper()

	got := 0
	for _, entity := range entities {
		if entity.Type == typ {
			got++
		}
	}
	if got != want {
		t.Fatalf("entity type %s count = %d, want %d", typ, got, want)
	}
}

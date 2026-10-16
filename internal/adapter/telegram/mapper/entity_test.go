package mapper

import (
	"testing"
	"unicode/utf8"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
	"github.com/gotd/td/tg"
)

func TestConvertEntitiesUsesUTF16OffsetsAndPreservesMalformed(t *testing.T) {
	text := "A😀B café"
	entities := []tg.MessageEntityClass{
		&tg.MessageEntityBold{Offset: 1, Length: 2},
		&tg.MessageEntityItalic{Offset: 5, Length: 4},
		&tg.MessageEntityUnderline{Offset: 2, Length: 1},
	}
	annotations, extensions, err := ConvertEntities(text, entities)
	if err != nil {
		t.Fatal(err)
	}
	if len(annotations) != 2 {
		t.Fatalf("annotations=%d", len(annotations))
	}
	if annotations[0].Offset != 1 || annotations[0].Length != 4 || annotations[0].Kind != otogi.AnnotationBold {
		t.Fatalf("emoji annotation=%+v", annotations[0])
	}
	if annotations[1].Offset != 7 || annotations[1].Length != 5 {
		t.Fatalf("accent annotation offset=%d length=%d", annotations[1].Offset, annotations[1].Length)
	}
	if len(extensions) != 1 || extensions[0].Name != "malformed-entity" {
		t.Fatalf("extensions=%v", extensions)
	}
}

func TestConvertEntitiesPreservesUnknownInOrder(t *testing.T) {
	entities := []tg.MessageEntityClass{
		&tg.MessageEntityUnknown{Offset: 0, Length: 1},
		&tg.MessageEntityBold{Offset: 0, Length: 1},
	}
	annotations, extensions, err := ConvertEntities("x", entities)
	if err != nil {
		t.Fatal(err)
	}
	if len(annotations) != 1 || len(extensions) != 1 {
		t.Fatalf("annotations=%d extensions=%d", len(annotations), len(extensions))
	}
	if extensions[0].Name != "unknown-entity" || len(extensions[0].Data) == 0 {
		t.Fatalf("unknown extension=%+v", extensions[0])
	}
}

func TestConvertEntitiesKeepsCollapsedQuotes(t *testing.T) {
	annotations, _, err := ConvertEntities("ab", []tg.MessageEntityClass{
		&tg.MessageEntityBlockquote{Offset: 0, Length: 1},
		&tg.MessageEntityBlockquote{Offset: 1, Length: 1, Collapsed: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if annotations[0].Kind != otogi.AnnotationBlockQuote || annotations[1].Kind != otogi.AnnotationExpandableBlockQuote {
		t.Fatalf("annotations = %+v", annotations)
	}
}

func FuzzConvertEntitiesUTF16(f *testing.F) {
	f.Add("A😀B", int32(1), int32(2))
	f.Add("café", int32(0), int32(4))
	f.Fuzz(func(t *testing.T, text string, offset, length int32) {
		if len(text) > 4096 {
			t.Skip()
		}
		annotations, _, err := ConvertEntities(text, []tg.MessageEntityClass{&tg.MessageEntityBold{Offset: int(offset), Length: int(length)}})
		if err != nil {
			return
		}
		for _, a := range annotations {
			end := a.Offset + a.Length
			if a.Offset < 0 || end > len(text) {
				t.Fatalf("out of range: %d..%d/%d", a.Offset, end, len(text))
			}
			boundary := func(i int) bool { return i == len(text) || utf8.RuneStart(text[i]) }
			if !boundary(a.Offset) || !boundary(end) {
				t.Fatalf("not UTF-8 boundaries")
			}
		}
	})
}

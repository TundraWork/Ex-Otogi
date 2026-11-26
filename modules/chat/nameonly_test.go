package chat

import (
	"cmp"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// rich builds text and its annotations as a driver would.
func rich(build func(r *otogi.RichText)) (string, []otogi.Annotation) {
	var r otogi.RichText
	build(&r)
	return r.String(), r.Annotations()
}

func sortedAnnotations(a []otogi.Annotation) []otogi.Annotation {
	a = slices.Clone(a)
	slices.SortFunc(a, func(x, y otogi.Annotation) int {
		return cmp.Or(cmp.Compare(x.Offset, y.Offset), cmp.Compare(x.Kind, y.Kind))
	})
	return a
}

// A sticker sent by its name alone reads as one, its annotation gone; the
// ranges after it move with it, and one around it grows with it.
func TestNameOnly(t *testing.T) {
	t.Parallel()
	face := func(r *otogi.RichText, name string) {
		r.Mark(otogi.AnnotationSticker, "qq:face:4", func() { r.Write(name) })
	}
	bold := func(r *otogi.RichText, write func()) { r.Mark(otogi.AnnotationBold, "", write) }
	link := func(r *otogi.RichText, s string) {
		r.Mark(otogi.AnnotationLink, "https://example.com", func() { r.Write(s) })
	}
	shown := "[sticker, picture not available: 被猪包围]"
	for _, tc := range []struct {
		name      string
		in, want  func(r *otogi.RichText)
		unchanged bool
	}{
		{
			name: "alone",
			in:   func(r *otogi.RichText) { face(r, "[被猪包围]") },
			want: func(r *otogi.RichText) { r.Write(shown) },
		},
		{
			name: "between text, before a link and bold",
			in: func(r *otogi.RichText) {
				r.Write("冲")
				face(r, "[被猪包围]")
				r.Write("！看")
				link(r, "这里")
				bold(r, func() { r.Write("快") })
			},
			want: func(r *otogi.RichText) {
				r.Write("冲" + shown + "！看")
				link(r, "这里")
				bold(r, func() { r.Write("快") })
			},
		},
		{
			name: "inside bold",
			in: func(r *otogi.RichText) {
				bold(r, func() {
					r.Write("看")
					face(r, "[被猪包围]")
					r.Write("啊")
				})
				link(r, "x")
			},
			want: func(r *otogi.RichText) {
				bold(r, func() { r.Write("看" + shown + "啊") })
				link(r, "x")
			},
		},
		{
			name: "bold just around it, and a link ending where it starts",
			in: func(r *otogi.RichText) {
				link(r, "a")
				bold(r, func() { face(r, "[被猪包围]") })
			},
			want: func(r *otogi.RichText) {
				link(r, "a")
				bold(r, func() { r.Write(shown) })
			},
		},
		{
			name: "two, one with no name",
			in: func(r *otogi.RichText) {
				face(r, "[被猪包围]")
				face(r, "[]")
				r.Write("!")
			},
			want: func(r *otogi.RichText) { r.Write(shown + "[sticker, picture not available]!") },
		},
		{
			name: "a name with no brackets",
			in:   func(r *otogi.RichText) { face(r, "被猪包围") },
			want: func(r *otogi.RichText) { r.Write(shown) },
		},
		{
			name: "an emoji is no sticker",
			in: func(r *otogi.RichText) {
				r.Mark(otogi.AnnotationEmoji, "qq:face:3:5", func() { r.Write("[流泪]") })
			},
			unchanged: true,
		},
	} {
		text, annotations := rich(tc.in)
		wantText, wantAnnotations := text, annotations
		if !tc.unchanged {
			wantText, wantAnnotations = rich(tc.want)
		}
		gotText, gotAnnotations := nameOnly(text, annotations)
		if gotText != wantText {
			t.Errorf("%s: text = %q, want %q", tc.name, gotText, wantText)
		}
		if got, want := sortedAnnotations(gotAnnotations), sortedAnnotations(wantAnnotations); !reflect.DeepEqual(got, want) && (len(got) > 0 || len(want) > 0) {
			t.Errorf("%s: annotations = %+v, want %+v", tc.name, got, want)
		}
	}
}

// A sticker sent by its name alone reads as one in the transcript, which
// the recent chat, judgments, quotes, and the memory read, and in the
// message answered.
func TestStickersSentByNameAlone(t *testing.T) {
	h := newHarness(t)
	market := func(prefix string) func(*otogi.Event) {
		return func(e *otogi.Event) {
			var r otogi.RichText
			r.Write(prefix + "冲")
			r.Mark(otogi.AnnotationSticker, "qq:face:4", func() { r.Write("[被猪包围]") })
			r.Write("！")
			e.Message = r.Message()
		}
	}
	h.mustHandle(h.message("Bob", "", market("")))
	h.mustHandle(h.message("Alice", "", market("otogi ")))
	text := lastUserText(h.llm["otogi"].requests[0])
	shown := "冲[sticker, picture not available: 被猪包围]！"
	if !strings.Contains(text, "Bob: "+shown) {
		t.Errorf("the recent chat lacks %q:\n%s", "Bob: "+shown, text)
	}
	if !strings.Contains(text, "otogi "+shown) {
		t.Errorf("the message answered lacks %q:\n%s", "otogi "+shown, text)
	}
	if strings.Contains(text, "冲[被猪包围]") {
		t.Errorf("the name reads as text:\n%s", text)
	}
}

// A range that starts or ends inside a sticker's name, which no driver
// writes, starts or ends with what replaces the name, so it neither breaks
// the replacement nor covers text it did not.
func TestNameOnlyPartlyOverlapping(t *testing.T) {
	t.Parallel()
	text := "冲[被猪包围]！"
	at, name := strings.Index(text, "["), len("[被猪包围]")
	in := []otogi.Annotation{
		{Offset: at, Length: name, Kind: otogi.AnnotationSticker, Target: "qq:face:4"},
		{Offset: 0, Length: at + len("[被"), Kind: otogi.AnnotationBold},
		{Offset: at + len("[被"), Length: len("猪包围]！"), Kind: otogi.AnnotationLink, Target: "https://example.com"},
	}
	shown := "[sticker, picture not available: 被猪包围]"
	gotText, got := nameOnly(text, in)
	if want := "冲" + shown + "！"; gotText != want {
		t.Fatalf("text = %q, want %q", gotText, want)
	}
	want := []otogi.Annotation{
		{Offset: 0, Length: at + len(shown), Kind: otogi.AnnotationBold},
		{Offset: at, Length: len(shown) + len("！"), Kind: otogi.AnnotationLink, Target: "https://example.com"},
	}
	if !reflect.DeepEqual(sortedAnnotations(got), sortedAnnotations(want)) {
		t.Fatalf("annotations = %+v, want %+v", got, want)
	}
}

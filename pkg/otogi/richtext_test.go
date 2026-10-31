package otogi

import (
	"reflect"
	"testing"
)

func TestRichTextSplit(t *testing.T) {
	t.Parallel()
	within := func(n int) func(string, int) bool {
		return func(prefix string, _ int) bool { return len(prefix) <= n }
	}
	cases := []struct {
		name        string
		build       func(r *RichText)
		fits        func(string, int) bool
		first, rest string
		firstMarks  []Annotation
		restMarks   []Annotation
	}{
		{"fits whole", func(r *RichText) { r.Write("short") }, within(10), "short", "", nil, nil},
		{"line end in the second half", func(r *RichText) { r.Write("aaaa\nbbbb\ncc") }, within(11), "aaaa\nbbbb", "cc", nil, nil},
		{"outside formatting", func(r *RichText) {
			r.Write("ab ")
			r.Mark(AnnotationBold, "", func() { r.Write("cdef") })
		}, within(5), "ab ", "cdef", nil, []Annotation{{Offset: 0, Length: 4, Kind: AnnotationBold}}},
		{"formatting continues across a forced cut", func(r *RichText) {
			r.Mark(AnnotationBold, "", func() { r.Write("abcdef") })
		}, within(4), "abcd", "ef", []Annotation{{Offset: 0, Length: 4, Kind: AnnotationBold}}, []Annotation{{Offset: 0, Length: 2, Kind: AnnotationBold}}},
		{"annotation limit", func(r *RichText) {
			r.Write("a")
			r.Mark(AnnotationBold, "", func() { r.Write("b") })
			r.Write(" c")
		}, func(_ string, n int) bool { return n == 0 }, "a", "b c", nil, []Annotation{{Offset: 0, Length: 1, Kind: AnnotationBold}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := &RichText{}
			c.build(r)
			first, rest := r.Split(c.fits)
			if first.String() != c.first || rest.String() != c.rest ||
				!reflect.DeepEqual(first.Annotations(), c.firstMarks) || !reflect.DeepEqual(rest.Annotations(), c.restMarks) {
				t.Fatalf("Split = %q %+v | %q %+v", first.String(), first.Annotations(), rest.String(), rest.Annotations())
			}
		})
	}
}

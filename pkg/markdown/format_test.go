package markdown

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// rich builds rich text from alternating plain strings and marks.
type mark struct {
	kind   otogi.AnnotationKind
	target string
	text   any // string or []any
}

func rich(pieces ...any) *otogi.RichText {
	r := &otogi.RichText{}
	var add func(p any)
	add = func(p any) {
		switch p := p.(type) {
		case string:
			r.Write(p)
		case mark:
			r.Mark(p.kind, p.target, func() { add(p.text) })
		case []any:
			for _, q := range p {
				add(q)
			}
		}
	}
	for _, p := range pieces {
		add(p)
	}
	return r
}

func TestFormat(t *testing.T) {
	t.Parallel()
	bold, italic := otogi.AnnotationBold, otogi.AnnotationItalic
	cases := []struct {
		name string
		in   *otogi.RichText
		want string
	}{
		{"plain text is not escaped", rich("2*3 = 6, a_b and [x]"), "2*3 = 6, a_b and [x]"},
		{"inline kinds", rich(mark{bold, "", "b"}, " ", mark{italic, "", "i"}, " ", mark{otogi.AnnotationStrike, "", "s"}, " ", mark{otogi.AnnotationCode, "", "c"}),
			"**b** _i_ ~~s~~ `c`"},
		{"no markdown form", rich(mark{otogi.AnnotationUnderline, "", "u"}, " ", mark{otogi.AnnotationSpoiler, "", "s"}, " ", mark{otogi.AnnotationMention, "telegram:user:1", "@x"}),
			"u s @x"},
		{"whitespace stays outside", rich("a", mark{bold, "", " b "}, "c"), "a **b** c"},
		{"same range nests", rich(mark{bold, "", mark{italic, "", "both"}}), "**_both_**"},
		{"overlap splits", rich(mark{bold, "", []any{"ab", mark{italic, "", "cd"}}}), "**ab_cd_**"},
		{"lines close markup", rich(mark{bold, "", "one\ntwo"}), "**one**\n**two**"},
		{"link", rich("see ", mark{otogi.AnnotationLink, "https://x.example/a", mark{bold, "", "docs"}}), "see [**docs**](https://x.example/a)"},
		{"link brackets and parens", rich(mark{otogi.AnnotationLink, "https://x.example/a (b)", "a [b]"}), `[a \[b\]](<https://x.example/a (b)>)`},
		{"code with backticks", rich(mark{otogi.AnnotationCode, "", "a`b"}), "``a`b``"},
		{"code starting with a backtick", rich(mark{otogi.AnnotationCode, "", "`x"}), "`` `x ``"},
		{"code in a link is not escaped", rich(mark{otogi.AnnotationLink, "u", mark{otogi.AnnotationCode, "", "a[0]"}}), "[`a[0]`](u)"},
		{"preformatted", rich("run:", mark{otogi.AnnotationPreformatted, "go", "x := 1\n```"}, "done"),
			"run:\n````go\nx := 1\n```\n````\ndone"},
		{"quote", rich(mark{otogi.AnnotationBlockQuote, "", "a\n\nb"}, "\nafter"), "> a\n>\n> b\n\nafter"},
		{"nested quote", rich(mark{otogi.AnnotationBlockQuote, "", []any{"a\n", mark{otogi.AnnotationExpandableBlockQuote, "", "b"}}}), "> a\n>> b"},
		{"quote mid-line", rich("he said: ", mark{otogi.AnnotationBlockQuote, "", "hi"}), "he said: \n> hi"},
		{"code block in a quote", rich(mark{otogi.AnnotationBlockQuote, "", mark{otogi.AnnotationPreformatted, "", "x\ny"}}), "> ```\n> x\n> y\n> ```"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := Format(c.in.String(), c.in.Annotations()); got != c.want {
				t.Errorf("Format:\n%s\nwant:\n%s", got, c.want)
			}
		})
	}
}

// TestFormatRoundTrip checks that Render reads back what Format writes, for
// rich text whose shape Markdown can express.
func TestFormatRoundTrip(t *testing.T) {
	t.Parallel()
	cases := map[string]*otogi.RichText{
		"inline": rich("Some ", mark{otogi.AnnotationItalic, "", "it"}, " and ", mark{otogi.AnnotationBold, "", "bold"}, ", ",
			mark{otogi.AnnotationStrike, "", "gone"}, ", ", mark{otogi.AnnotationCode, "", "x := 1"}, " and ",
			mark{otogi.AnnotationLink, "https://d.example/a", "docs"}, "."),
		"nested":      rich(mark{otogi.AnnotationBold, "", []any{"a ", mark{otogi.AnnotationItalic, "", "b"}, " c"}}),
		"link text":   rich(mark{otogi.AnnotationLink, "https://x.example/(a)", "list [1]"}),
		"code ticks":  rich("use ", mark{otogi.AnnotationCode, "", "a``b"}),
		"code block":  rich("first\n\n", mark{otogi.AnnotationPreformatted, "go", "fmt.Println()\n\n// done"}, "\n\nlast"),
		"quote":       rich(mark{otogi.AnnotationBlockQuote, "", []any{"quoted ", mark{otogi.AnnotationBold, "", "b"}}}, "\n\nafter"),
		"unicode":     rich("日本 ", mark{otogi.AnnotationBold, "", "語"}, " 🎉"),
		"plain chars": rich("2*3 and a_b"),
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			md := Format(in.String(), in.Annotations())
			if got, want := show(Render(md)), show(in); got != want {
				t.Errorf("Render(Format) via %q:\n%s\nwant:\n%s", md, got, want)
			}
		})
	}
}

// FuzzFormat checks that Format never panics and only adds markup: the text
// comes back out, in order, from any annotations.
func FuzzFormat(f *testing.F) {
	f.Add("hello *world*\n> x `y`", 0, 5, 3, 7, 2, 9)
	f.Add("日本語 [a](b)\n\n```", 1, 3, 0, 12, 5, 6)
	kinds := []otogi.AnnotationKind{
		otogi.AnnotationBold, otogi.AnnotationItalic, otogi.AnnotationStrike, otogi.AnnotationCode,
		otogi.AnnotationLink, otogi.AnnotationPreformatted, otogi.AnnotationBlockQuote, otogi.AnnotationSpoiler,
	}
	f.Fuzz(func(t *testing.T, text string, a, b, c, d, e, g int) {
		if !utf8.ValidString(text) {
			return
		}
		var annotations []otogi.Annotation
		nums := []int{a, b, c, d, e, g}
		for i := 0; i+1 < len(nums); i += 2 {
			start, end := boundary(text, nums[i]), boundary(text, nums[i+1])
			if start > end {
				start, end = end, start
			}
			annotations = append(annotations, otogi.Annotation{Offset: start, Length: end - start, Kind: kinds[abs(nums[i]+nums[i+1])%len(kinds)], Target: "u"})
		}
		out := Format(text, annotations)
		rest := out
		for _, r := range text {
			i := strings.IndexRune(rest, r)
			if i < 0 {
				t.Fatalf("Format(%q, %+v) = %q lost %q", text, annotations, out, r)
			}
			rest = rest[i+utf8.RuneLen(r):]
		}
	})
}

// boundary maps n to a UTF-8 boundary within s.
func boundary(s string, n int) int {
	if len(s) == 0 {
		return 0
	}
	n = abs(n) % (len(s) + 1)
	for n < len(s) && !utf8.RuneStart(s[n]) {
		n++
	}
	return n
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

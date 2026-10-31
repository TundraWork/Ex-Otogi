// Package markdown converts between Markdown (CommonMark with GitHub's
// strikethrough, tables, and bare-URL links) and otogi rich text, so a module
// can post Markdown written elsewhere, such as a language model's reply, as
// formatted chat text, and hand formatted chat text to a model.
package markdown

import (
	"cmp"
	"slices"
	"strconv"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
	"golang.org/x/text/width"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

var parser = goldmark.New(goldmark.WithExtensions(extension.Strikethrough, extension.Table, extension.Linkify)).Parser()

// Render converts source to rich text:
//   - emphasis to italic and bold, ~~text~~ to strike;
//   - code spans to code, code blocks to preformatted (with their language);
//   - links, autolinks, and bare URLs to links, images to links on their alt text;
//   - block quotes to block quotes, headings to bold lines;
//   - list items to "• " or "1. " lines, indented by nesting depth;
//   - tables to preformatted blocks with aligned columns, keeping the URL of
//     each link after its text, since chat platforms have no tables.
//
// Blocks are separated by a blank line; soft line breaks are kept, because
// chat text is laid out by line. Raw HTML is kept as text.
func Render(source string) *otogi.RichText {
	src := []byte(source)
	r := &renderer{src: src, out: &otogi.RichText{}}
	r.children(parser.Parse(text.NewReader(src)), "\n\n", 0)
	return r.out
}

type renderer struct {
	src []byte
	out *otogi.RichText
}

// children renders the block children of n, separated by sep.
func (r *renderer) children(n ast.Node, sep string, depth int) {
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		if c != n.FirstChild() {
			r.out.Write(sep)
		}
		r.block(c, depth)
	}
}

func (r *renderer) block(n ast.Node, depth int) {
	switch n := n.(type) {
	case *ast.Paragraph, *ast.TextBlock:
		r.inlines(n)
	case *ast.Heading:
		r.out.Mark(otogi.AnnotationBold, "", func() { r.inlines(n) })
	case *ast.Blockquote:
		r.out.Mark(otogi.AnnotationBlockQuote, "", func() { r.children(n, "\n\n", depth) })
	case *ast.FencedCodeBlock:
		r.out.Mark(otogi.AnnotationPreformatted, string(n.Language(r.src)), func() { r.lines(n) })
	case *ast.CodeBlock:
		r.out.Mark(otogi.AnnotationPreformatted, "", func() { r.lines(n) })
	case *ast.HTMLBlock:
		r.lines(n)
	case *east.Table:
		r.out.Mark(otogi.AnnotationPreformatted, "", func() { r.out.Write(r.table(n)) })
	case *ast.ThematicBreak:
		r.out.Write("———")
	case *ast.List:
		number := n.Start
		for item := n.FirstChild(); item != nil; item = item.NextSibling() {
			if item != n.FirstChild() {
				r.out.Write("\n")
			}
			r.out.Write(strings.Repeat("  ", depth))
			if n.IsOrdered() {
				r.out.Write(strconv.Itoa(number) + ". ")
				number++
			} else {
				r.out.Write("• ")
			}
			r.children(item, "\n", depth+1)
		}
	default:
		r.children(n, "\n\n", depth)
	}
}

// lines writes the raw lines of a code or HTML block, without the final
// newline.
func (r *renderer) lines(n ast.Node) {
	var b strings.Builder
	lines := n.Lines()
	for i := range lines.Len() {
		segment := lines.At(i)
		b.Write(segment.Value(r.src))
	}
	r.out.Write(strings.TrimRight(b.String(), "\n"))
}

func (r *renderer) inlines(n ast.Node) {
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		r.inline(c)
	}
}

func (r *renderer) inline(n ast.Node) {
	switch n := n.(type) {
	case *ast.Text:
		value := n.Segment.Value(r.src)
		if !n.IsRaw() {
			value = util.ResolveEntityNames(util.ResolveNumericReferences(util.UnescapePunctuations(value)))
		}
		r.out.Write(string(value))
		if n.SoftLineBreak() || n.HardLineBreak() {
			r.out.Write("\n")
		}
	case *ast.String:
		r.out.Write(string(n.Value))
	case *ast.CodeSpan:
		var b strings.Builder
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			if t, ok := c.(*ast.Text); ok {
				b.Write(t.Segment.Value(r.src))
			}
		}
		r.out.Mark(otogi.AnnotationCode, "", func() { r.out.Write(b.String()) })
	case *ast.Emphasis:
		kind := otogi.AnnotationItalic
		if n.Level >= 2 {
			kind = otogi.AnnotationBold
		}
		r.out.Mark(kind, "", func() { r.inlines(n) })
	case *east.Strikethrough:
		r.out.Mark(otogi.AnnotationStrike, "", func() { r.inlines(n) })
	case *ast.Link:
		r.out.Mark(otogi.AnnotationLink, string(n.Destination), func() { r.inlines(n) })
	case *ast.Image:
		r.out.Mark(otogi.AnnotationLink, string(n.Destination), func() { r.inlines(n) })
	case *ast.AutoLink:
		url := string(n.URL(r.src))
		if n.AutoLinkType == ast.AutoLinkEmail && !strings.HasPrefix(url, "mailto:") {
			url = "mailto:" + url
		}
		r.out.Mark(otogi.AnnotationLink, url, func() { r.out.Write(string(n.Label(r.src))) })
	case *ast.RawHTML:
		for i := range n.Segments.Len() {
			segment := n.Segments.At(i)
			r.out.Write(string(segment.Value(r.src)))
		}
	default:
		r.inlines(n)
	}
}

// table lays out a table as text in aligned columns.
func (r *renderer) table(n *east.Table) string {
	var rows [][]string
	var widths []int
	for row := n.FirstChild(); row != nil; row = row.NextSibling() {
		var cells []string
		for c := row.FirstChild(); c != nil; c = c.NextSibling() {
			text := r.cell(c)
			if len(cells) == len(widths) {
				widths = append(widths, 0)
			}
			widths[len(cells)] = max(widths[len(cells)], displayWidth(text))
			cells = append(cells, text)
		}
		rows = append(rows, cells)
	}
	var b strings.Builder
	line := func(cells []string) {
		var l strings.Builder
		for i, text := range cells {
			if i > 0 {
				l.WriteString(" | ")
			}
			pad := widths[i] - displayWidth(text)
			left := 0
			if i < len(n.Alignments) {
				switch n.Alignments[i] {
				case east.AlignRight:
					left = pad
				case east.AlignCenter:
					left = pad / 2
				}
			}
			l.WriteString(strings.Repeat(" ", left) + text + strings.Repeat(" ", pad-left))
		}
		b.WriteString(strings.TrimRight(l.String(), " "))
	}
	for i, cells := range rows {
		if i > 0 {
			b.WriteString("\n")
		}
		line(cells)
		if i == 0 {
			rule := make([]string, len(widths))
			for j, w := range widths {
				rule[j] = strings.Repeat("-", w)
			}
			b.WriteString("\n" + strings.Join(rule, "-+-"))
		}
	}
	return b.String()
}

// cell is a table cell's text, with each link's URL after its text.
func (r *renderer) cell(n ast.Node) string {
	sub := &renderer{src: r.src, out: &otogi.RichText{}}
	sub.inlines(n)
	text := sub.out.String()
	links := slices.DeleteFunc(slices.Clone(sub.out.Annotations()), func(a otogi.Annotation) bool {
		label := text[a.Offset : a.Offset+a.Length]
		return a.Kind != otogi.AnnotationLink || a.Target == label || a.Target == "mailto:"+label
	})
	slices.SortFunc(links, func(a, b otogi.Annotation) int { return cmp.Compare(a.Offset, b.Offset) })
	var b strings.Builder
	last := 0
	for _, a := range links {
		end := a.Offset + a.Length
		b.WriteString(text[last:end] + " (" + a.Target + ")")
		last = end
	}
	b.WriteString(text[last:])
	return b.String()
}

// displayWidth counts the columns text takes in a monospace font, where
// East Asian wide characters take two.
func displayWidth(text string) int {
	n := 0
	for _, c := range text {
		switch width.LookupRune(c).Kind() {
		case width.EastAsianWide, width.EastAsianFullwidth:
			n += 2
		default:
			n++
		}
	}
	return n
}

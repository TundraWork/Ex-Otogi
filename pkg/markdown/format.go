package markdown

import (
	"cmp"
	"slices"
	"strings"
	"unicode"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// Format converts annotated text into Markdown, the inverse of Render, so
// formatted chat text can be handed to a language model:
//   - bold, italic, and strike to **, _, and ~~; code to code spans;
//   - links to [text](url);
//   - preformatted to fenced code blocks with their language;
//   - block quotes, expandable ones too, to "> " lines.
//
// Other kinds (underline, spoiler, mention, and so on) have no Markdown form
// and keep only their text. Format only adds markup: the text itself is not
// escaped, so a model reads what the user wrote, and removing the markup
// gives the text back. Overlapping annotations are split so they nest, and
// markup never starts or ends on whitespace or crosses a line. Annotations
// must lie within text on UTF-8 boundaries, as otogi.Message.Validate
// requires.
func Format(text string, annotations []otogi.Annotation) string {
	cuts := []int{0, len(text)}
	for _, a := range annotations {
		cuts = append(cuts, a.Offset, a.Offset+a.Length)
	}
	for i, c := range text {
		if c == '\n' {
			cuts = append(cuts, i, i+1)
		}
	}
	slices.Sort(cuts)
	cuts = slices.Compact(cuts)
	f := formatter{lineStart: true}
	for i := 0; i+1 < len(cuts); i++ {
		from, to := cuts[i], cuts[i+1]
		f.segment(text[from:to], activeAt(annotations, from, to))
	}
	f.closeTo(0)
	f.text(f.pending)
	f.endPre()
	return f.out.String()
}

// marker is an inline annotation that Markdown can express.
type marker struct {
	kind   otogi.AnnotationKind
	target string
}

// inlineOrder nests markers: a link outermost, a code span innermost,
// because nothing can be marked up inside a code span.
var inlineOrder = map[otogi.AnnotationKind]int{
	otogi.AnnotationLink: 0, otogi.AnnotationBold: 1, otogi.AnnotationItalic: 2,
	otogi.AnnotationStrike: 3, otogi.AnnotationCode: 4,
}

// active is what applies to one segment of the text.
type active struct {
	inline []marker // in inlineOrder, one per kind
	pre    *marker  // the preformatted block, if any
	quotes int      // block quote depth
}

func activeAt(annotations []otogi.Annotation, from, to int) active {
	var act active
	for _, a := range annotations {
		if a.Offset > from || a.Offset+a.Length < to {
			continue
		}
		switch a.Kind {
		case otogi.AnnotationPreformatted:
			if act.pre == nil {
				act.pre = &marker{a.Kind, a.Target}
			}
		case otogi.AnnotationBlockQuote, otogi.AnnotationExpandableBlockQuote:
			act.quotes++
		default:
			if _, ok := inlineOrder[a.Kind]; ok && !slices.ContainsFunc(act.inline, func(m marker) bool { return m.kind == a.Kind }) {
				act.inline = append(act.inline, marker{a.Kind, a.Target})
			}
		}
	}
	slices.SortFunc(act.inline, func(a, b marker) int { return cmp.Compare(inlineOrder[a.kind], inlineOrder[b.kind]) })
	return act
}

type formatter struct {
	out       strings.Builder
	open      []marker         // inline markers open, outermost first
	code      *strings.Builder // the open code span's text
	pending   string           // trailing whitespace held back so markers close before it
	pre       *marker          // the open preformatted block
	preText   strings.Builder
	quotes    int  // block quote depth of the current line
	lineStart bool // the output is at the start of a line
	breaks    int  // line breaks since the last content
	blank     bool // the next content needs a blank line before it, after a quote
	newline   bool // the next content needs a line of its own, after a code block
}

func (f *formatter) segment(s string, act active) {
	if s == "\n" {
		act.inline = nil // markup never crosses a line
	}
	if act.pre != nil {
		act.inline = nil
	}
	preChanged := (act.pre == nil) != (f.pre == nil) || act.pre != nil && *act.pre != *f.pre
	if preChanged || act.quotes != f.quotes {
		f.closeTo(0)
		f.endPre()
	}
	if act.quotes != f.quotes {
		f.text(f.pending)
		f.pending = ""
		if !f.lineStart {
			f.raw("\n")
		}
		if act.quotes < f.quotes {
			f.blank = true
		}
		f.quotes = act.quotes
	}
	if act.pre != nil {
		if f.pre == nil {
			f.text(f.pending)
			f.pending = ""
			f.pre = act.pre
		}
		f.preText.WriteString(s)
		return
	}

	core := strings.TrimFunc(s, unicode.IsSpace)
	if core == "" { // whitespace opens nothing
		f.closeTo(commonPrefix(f.open, act.inline))
		f.pending += s
		return
	}
	lead := s[:len(s)-len(strings.TrimLeftFunc(s, unicode.IsSpace))]
	trail := s[len(lead)+len(core):]
	keep := commonPrefix(f.open, act.inline)
	f.closeTo(keep)
	if keep < len(act.inline) {
		f.text(f.pending + lead)
		for _, m := range act.inline[keep:] {
			f.openMarker(m)
		}
		f.text(core)
	} else {
		f.text(f.pending + lead + core)
	}
	f.pending = trail
}

func commonPrefix(a, b []marker) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

func (f *formatter) openMarker(m marker) {
	f.open = append(f.open, m)
	switch m.kind {
	case otogi.AnnotationLink:
		f.write("[")
	case otogi.AnnotationCode:
		f.code = &strings.Builder{}
	default:
		f.write(delimiters[m.kind])
	}
}

var delimiters = map[otogi.AnnotationKind]string{
	otogi.AnnotationBold: "**", otogi.AnnotationItalic: "_", otogi.AnnotationStrike: "~~",
}

// closeTo closes open markers until n remain.
func (f *formatter) closeTo(n int) {
	for len(f.open) > n {
		m := f.open[len(f.open)-1]
		f.open = f.open[:len(f.open)-1]
		switch m.kind {
		case otogi.AnnotationLink:
			f.write("](" + linkDestination(m.target) + ")")
		case otogi.AnnotationCode:
			text := f.code.String()
			f.code = nil
			fence := strings.Repeat("`", longestRun(text, '`')+1)
			if strings.HasPrefix(text, "`") || strings.HasSuffix(text, "`") {
				text = " " + text + " "
			}
			f.write(fence + text + fence)
		default:
			f.write(delimiters[m.kind])
		}
	}
}

// linkDestination writes a URL so that spaces and parentheses in it cannot
// end the link early.
func linkDestination(url string) string {
	if url != "" && !strings.ContainsAny(url, " ()<>") {
		return url
	}
	return "<" + strings.NewReplacer("<", "%3C", ">", "%3E").Replace(url) + ">"
}

// endPre writes the open preformatted block as a fenced code block.
func (f *formatter) endPre() {
	if f.pre == nil {
		return
	}
	text := strings.TrimSuffix(f.preText.String(), "\n")
	fence := strings.Repeat("`", max(3, longestRun(text, '`')+1))
	if !f.lineStart {
		f.newline = true
	}
	f.write(fence + f.pre.target + "\n" + text + "\n" + fence)
	f.pre = nil
	f.preText.Reset()
	f.newline = true
}

// text writes content: into the open code span, or as text, where brackets
// are escaped inside a link so they cannot end its text.
func (f *formatter) text(s string) {
	if f.code != nil {
		f.code.WriteString(s)
		return
	}
	if slices.ContainsFunc(f.open, func(m marker) bool { return m.kind == otogi.AnnotationLink }) {
		s = strings.NewReplacer("[", `\[`, "]", `\]`).Replace(s)
	}
	f.write(s)
}

// write writes markup or text onto the current line, after any separation
// that a block closed before it requires.
func (f *formatter) write(s string) {
	if s == "" {
		return
	}
	if content := strings.TrimLeft(s, "\n"); content != "" && (f.blank || f.newline) {
		want := 1
		if f.blank {
			want = 2
		}
		for f.out.Len() > 0 && f.breaks+len(s)-len(content) < want {
			f.raw("\n")
		}
		f.blank, f.newline = false, false
	}
	f.raw(s)
}

// raw writes s, starting each line inside a block quote with its prefix.
func (f *formatter) raw(s string) {
	prefix := strings.Repeat(">", f.quotes)
	for s != "" {
		line, rest, found := strings.Cut(s, "\n")
		if f.lineStart && f.quotes > 0 {
			if line == "" {
				f.out.WriteString(prefix)
			} else {
				f.out.WriteString(prefix + " ")
			}
		}
		f.out.WriteString(line)
		f.lineStart = false
		if line != "" {
			f.breaks = 0
		}
		if found {
			f.out.WriteString("\n")
			f.lineStart = true
			f.breaks++
		}
		s = rest
	}
}

func longestRun(s string, c rune) int {
	longest, run := 0, 0
	for _, r := range s {
		if r == c {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	return longest
}

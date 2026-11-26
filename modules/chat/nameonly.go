package chat

import (
	"slices"
	"strings"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// nameOnly writes each sticker a platform sent by its name alone
// (AnnotationSticker, a QQ market face) as what it is, [sticker, picture
// not available: name], so a model does not take the name for text or
// wait for a picture. Its annotation goes, and the others' ranges move to
// match: one that holds the sticker's name holds what replaces it.
func nameOnly(text string, annotations []otogi.Annotation) (string, []otogi.Annotation) {
	var stickers []otogi.Annotation
	for _, a := range annotations {
		if a.Kind == otogi.AnnotationSticker && a.Offset >= 0 && a.Length > 0 && a.Offset+a.Length <= len(text) {
			stickers = append(stickers, a)
		}
	}
	if len(stickers) == 0 {
		return text, annotations
	}
	slices.SortStableFunc(stickers, func(a, b otogi.Annotation) int { return a.Offset - b.Offset })
	// An edit replaces text[from:to] with what ends at end in the new text.
	type edit struct{ from, to, start, end int }
	var (
		b     strings.Builder
		edits []edit
		last  int
	)
	for _, st := range stickers {
		if st.Offset < last { // overlapping the one before: left as it is
			continue
		}
		b.WriteString(text[last:st.Offset])
		start := b.Len()
		b.WriteString(stickerName(text[st.Offset : st.Offset+st.Length]))
		edits = append(edits, edit{st.Offset, st.Offset + st.Length, start, b.Len()})
		last = st.Offset + st.Length
	}
	b.WriteString(text[last:])
	// moved is where pos, the start or the end of a range, is in the new
	// text: within a replaced name, at the start or end of what replaced it.
	moved := func(pos int, end bool) int {
		shift := 0
		for _, e := range edits {
			switch {
			case pos <= e.from:
				return pos + shift
			case pos >= e.to:
				shift = e.end - e.to
			case end:
				return e.end
			default:
				return e.start
			}
		}
		return pos + shift
	}
	var out []otogi.Annotation
	for _, a := range annotations {
		if a.Kind == otogi.AnnotationSticker && slices.ContainsFunc(edits, func(e edit) bool { return e.from == a.Offset && e.to == a.Offset+a.Length }) {
			continue
		}
		start, end := moved(a.Offset, false), moved(a.Offset+a.Length, true)
		a.Offset, a.Length = start, end-start
		out = append(out, a)
	}
	return b.String(), out
}

// stickerName is how a sticker sent by name alone reads: the name, without
// the brackets QQ writes around it.
func stickerName(name string) string {
	name = strings.TrimSpace(name)
	if strings.HasPrefix(name, "[") && strings.HasSuffix(name, "]") {
		name = strings.TrimSpace(name[1 : len(name)-1])
	}
	if name == "" {
		return "[sticker, picture not available]"
	}
	return "[sticker, picture not available: " + name + "]"
}

package todou

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// messageLimit keeps a message under Telegram's 4096 UTF-16 units, with room
// for a little platform overhead.
const messageLimit = 4000

// renderer turns watch entries into notification messages.
type renderer struct {
	linkBase string // web base URL for issue links
	maxBody  int    // characters of a body to quote
	title    func(project string, number int64) issue
}

// block is one entry's rendered text with its annotations (offsets relative
// to the block).
type block struct {
	text        strings.Builder
	annotations []otogi.Annotation
}

func (b *block) write(s string) { b.text.WriteString(s) }

func (b *block) annotated(s string, kind otogi.AnnotationKind, target string) {
	b.annotations = append(b.annotations, otogi.Annotation{Offset: b.text.Len(), Length: len(s), Kind: kind, Target: target})
	b.text.WriteString(s)
}

// messages renders entries, in order, into as few messages as fit.
func (r renderer) messages(entries []entry) []otogi.Message {
	var out []otogi.Message
	var text strings.Builder
	var annotations []otogi.Annotation
	flush := func() {
		if text.Len() > 0 {
			out = append(out, otogi.Message{Roots: []otogi.PartID{"text"}, Parts: []otogi.Part{{ID: "text", Kind: otogi.PartText, Text: text.String(), Annotations: annotations}}})
		}
		text.Reset()
		annotations = nil
	}
	for _, e := range entries {
		b := r.block(e)
		separator := ""
		if text.Len() > 0 {
			separator = "\n\n"
		}
		if utf16Len(text.String())+utf16Len(separator)+utf16Len(b.text.String()) > messageLimit {
			flush()
			separator = ""
		}
		text.WriteString(separator)
		base := text.Len()
		for _, a := range b.annotations {
			a.Offset += base
			annotations = append(annotations, a)
		}
		text.WriteString(b.text.String())
	}
	flush()
	return out
}

func (r renderer) block(e entry) *block {
	b := &block{}
	info := r.title(e.Project, e.IssueNumber)
	ref := e.IssueRef
	if ref == "" {
		ref = e.Project + "#" + strconv.FormatInt(e.IssueNumber, 10)
	}
	who := e.Author
	if e.Type != "comment" {
		who = e.Actor
	}
	name := who.name()
	if e.AgentContext != nil && e.AgentContext.Agent != "" {
		name += " (" + e.AgentContext.Agent + ")"
	}

	icon, action, body := r.describe(e, info)
	b.write(icon + " ")
	b.annotated(ref, otogi.AnnotationLink, fmt.Sprintf("%s/projects/%s/issues/%d", r.linkBase, e.Project, e.IssueNumber))
	if info.Title != "" {
		b.write(" ")
		b.annotated("“"+info.Title+"”", otogi.AnnotationBold, "")
	}
	b.write(" — " + name + " " + action)
	if body != "" {
		b.write(":\n" + indent(r.cut(body)))
	}
	return b
}

// describe returns the icon, the action phrase, and an optional body to quote.
func (r renderer) describe(e entry, info issue) (icon, action, body string) {
	if e.Type == "comment" {
		if e.HiddenAt != nil {
			return "💬", "commented (hidden comment)", ""
		}
		return "💬", "commented", e.Body
	}
	p := e.Payload
	switch e.EventType {
	case "opened":
		return "🆕", "opened it", info.Body
	case "closed":
		return "✅", "closed it", ""
	case "reopened":
		return "🔁", "reopened it", ""
	case "status_changed":
		return "🔀", fmt.Sprintf("moved it %s → %s", field(p, "from", "name"), field(p, "to", "name")), ""
	case "title_changed":
		return "✏️", fmt.Sprintf("renamed it from “%s”", str(p["from"])), ""
	case "assigned":
		return "👤", "assigned " + field(p, "user", "login"), ""
	case "unassigned":
		return "👤", "unassigned " + field(p, "user", "login"), ""
	case "label_added":
		return "🏷", "added label " + field(p, "label", "name"), ""
	case "label_removed":
		return "🏷", "removed label " + field(p, "label", "name"), ""
	case "spec_pushed":
		action = "pushed spec v" + str(p["version"])
		return "📄", action, str(p["message"])
	case "spec_review":
		return "📝", fmt.Sprintf("reviewed spec v%s: %s", str(p["version"]), strings.ReplaceAll(str(p["verdict"]), "_", " ")), ""
	case "spec_withdrawn":
		return "📄", "withdrew spec v" + str(p["version"]), ""
	case "question_answered":
		return "❓", "answered a question", ""
	case "deleted":
		return "🗑", "deleted it", ""
	case "restored":
		return "♻️", "restored it", ""
	default:
		return "•", strings.ReplaceAll(e.EventType, "_", " "), ""
	}
}

// cut shortens s to maxBody characters, on a rune boundary.
func (r renderer) cut(s string) string {
	s = strings.TrimSpace(s)
	runes := []rune(s)
	if r.maxBody > 0 && len(runes) > r.maxBody {
		return string(runes[:r.maxBody]) + "…"
	}
	return s
}

func indent(s string) string { return "  " + strings.ReplaceAll(s, "\n", "\n  ") }

func utf16Len(s string) int { return len(utf16.Encode([]rune(s))) }

func str(v any) string {
	switch v := v.(type) {
	case nil:
		return "?"
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return fmt.Sprint(v)
	}
}

// field reads payload[key][name], e.g. the name of a status.
func field(p map[string]any, key, name string) string {
	if m, ok := p[key].(map[string]any); ok {
		return str(m[name])
	}
	return "?"
}

package todou

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/TundraWork/Ex-Otogi/pkg/markdown"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// messageLimit keeps a message under Telegram's 4096 UTF-16 units, with room
// for a little platform overhead.
const messageLimit = 4000

// renderer turns queued entries into digest messages.
type renderer struct {
	linkBase string // web base URL for issue links
	maxBody  int    // UTF-16 units of a body to quote
}

// digest renders entries, oldest first, as one digest: a count line when
// there are several, then the entries grouped by issue in order of first
// appearance, each issue under a line with its linked reference and title.
// A digest too long for one message continues in the next, repeating the
// issue line. issues holds what is known about each issue by titleKey.
func (r renderer) digest(entries []entry, issues map[string]issue) []otogi.Message {
	var groups [][]entry
	index := map[string]int{}
	for _, e := range entries {
		key := titleKey(e.Project, e.IssueNumber)
		i, ok := index[key]
		if !ok {
			i, index[key] = len(groups), len(groups)
			groups = append(groups, nil)
		}
		groups[i] = append(groups[i], e)
	}

	var out []otogi.Message
	msg := &otogi.RichText{}
	if len(entries) > 1 {
		msg.Mark(otogi.AnnotationBold, "", func() { msg.Write(fmt.Sprintf("🔔 %d todou updates", len(entries))) })
	}
	for _, g := range groups {
		info := issues[titleKey(g[0].Project, g[0].IssueNumber)]
		head := r.issueLine(g[0], info)
		for i, e := range g {
			chunk := &otogi.RichText{}
			if i == 0 {
				add(chunk, head)
				chunk.Write("\n")
			}
			add(chunk, r.entry(e, info))
			sep := "\n"
			if i == 0 {
				sep = "\n\n"
			}
			if msg.Len() > 0 && utf16Len(msg.String()+sep+chunk.String()) > messageLimit {
				out = append(out, msg.Message())
				msg = &otogi.RichText{}
				if i > 0 {
					continued := &otogi.RichText{}
					add(continued, head)
					continued.Write("\n")
					add(continued, chunk)
					chunk = continued
				}
			}
			if msg.Len() > 0 {
				msg.Write(sep)
			}
			add(msg, chunk)
		}
	}
	if msg.Len() > 0 {
		out = append(out, msg.Message())
	}
	return out
}

func add(dst, src *otogi.RichText) { dst.Append(src.String(), src.Annotations()) }

// issueLine is the linked reference and the bold title of an entry's issue.
func (r renderer) issueLine(e entry, info issue) *otogi.RichText {
	t := &otogi.RichText{}
	ref := e.IssueRef
	if ref == "" {
		ref = e.Project + "#" + strconv.FormatInt(e.IssueNumber, 10)
	}
	t.Mark(otogi.AnnotationLink, fmt.Sprintf("%s/projects/%s/issues/%d", r.linkBase, e.Project, e.IssueNumber), func() { t.Write(ref) })
	if info.Title != "" {
		t.Write(" ")
		t.Mark(otogi.AnnotationBold, "", func() { t.Write(info.Title) })
	}
	return t
}

// entry is one entry's line, with its body quoted underneath.
func (r renderer) entry(e entry, info issue) *otogi.RichText {
	who := e.Author
	if e.Type != "comment" {
		who = e.Actor
	}
	name := who.name()
	if e.AgentContext != nil && e.AgentContext.Agent != "" {
		name += " (" + e.AgentContext.Agent + ")"
	}
	icon, action, body := describe(e, info)
	t := &otogi.RichText{}
	t.Write(icon + " " + name + " ")
	add(t, action)
	if strings.TrimSpace(body) != "" {
		t.Write("\n")
		t.Mark(otogi.AnnotationBlockQuote, "", func() { add(t, r.quote(body)) })
	}
	return t
}

// code and bold are phrase parts shown as inline code and bold.
type (
	code string
	bold string
)

func phrase(parts ...any) *otogi.RichText {
	t := &otogi.RichText{}
	for _, p := range parts {
		switch p := p.(type) {
		case code:
			t.Mark(otogi.AnnotationCode, "", func() { t.Write(string(p)) })
		case bold:
			t.Mark(otogi.AnnotationBold, "", func() { t.Write(string(p)) })
		default:
			t.Write(fmt.Sprint(p))
		}
	}
	return t
}

// describe returns the icon, the action phrase, and an optional Markdown
// body to quote.
func describe(e entry, info issue) (icon string, action *otogi.RichText, body string) {
	if e.Type == "comment" {
		if e.HiddenAt != nil {
			return "💬", phrase("commented (hidden)"), ""
		}
		return "💬", phrase("commented"), e.Body
	}
	p := e.Payload
	switch e.EventType {
	case "opened":
		return "🆕", phrase("opened it"), info.Body
	case "closed":
		return "✅", phrase("closed it"), ""
	case "reopened":
		return "🔁", phrase("reopened it"), ""
	case "status_changed":
		return "🔀", phrase("moved it ", code(field(p, "from", "name")), " → ", code(field(p, "to", "name"))), ""
	case "title_changed":
		return "✏️", phrase("renamed it from “", str(p["from"]), "”"), ""
	case "assigned":
		return "👤", phrase("assigned ", field(p, "user", "login")), ""
	case "unassigned":
		return "👤", phrase("unassigned ", field(p, "user", "login")), ""
	case "label_added":
		return "🏷", phrase("added label ", code(field(p, "label", "name"))), ""
	case "label_removed":
		return "🏷", phrase("removed label ", code(field(p, "label", "name"))), ""
	case "spec_pushed":
		return "📄", phrase("pushed spec ", code("v"+str(p["version"]))), str(p["message"])
	case "spec_review":
		return "📝", phrase("reviewed spec ", code("v"+str(p["version"])), ": ", bold(strings.ReplaceAll(str(p["verdict"]), "_", " "))), ""
	case "spec_withdrawn":
		return "📄", phrase("withdrew spec ", code("v"+str(p["version"]))), ""
	case "question_answered":
		return "❓", phrase("answered a question"), ""
	case "deleted":
		return "🗑", phrase("deleted it"), ""
	case "restored":
		return "♻️", phrase("restored it"), ""
	default:
		return "•", phrase(strings.ReplaceAll(e.EventType, "_", " ")), ""
	}
}

// quote renders a Markdown body for the quote under an entry, cut to maxBody
// UTF-16 units and without block quotes of its own: chats do not nest quotes.
func (r renderer) quote(body string) *otogi.RichText {
	md := markdown.Render(strings.TrimSpace(body))
	text := md.String()
	end, units := len(text), 0
	for i, c := range text {
		if units += utf16.RuneLen(c); units > r.maxBody {
			end = len(strings.TrimRight(text[:i], " \t\n"))
			break
		}
	}
	var kept []otogi.Annotation
	for _, a := range md.Annotations() {
		if a.Kind == otogi.AnnotationBlockQuote || a.Offset >= end {
			continue
		}
		a.Length = min(a.Length, end-a.Offset)
		kept = append(kept, a)
	}
	out := &otogi.RichText{}
	out.Append(text[:end], kept)
	if end < len(text) {
		out.Write("…")
	}
	return out
}

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

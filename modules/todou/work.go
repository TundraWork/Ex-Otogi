package todou

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/TundraWork/Ex-Otogi/pkg/markdown"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// refusal is a request the module turns down, said to the person (or the
// model) in plain words; it is not a failure.
type refusal struct{ msg string }

func (r *refusal) Error() string { return r.msg }

func refuse(format string, args ...any) error { return &refusal{fmt.Sprintf(format, args...)} }

// target is a card a chat may work with: in a project linked to the chat,
// read through the agent of that link.
type target struct {
	agent   *agent
	project string
	card    card
}

func (t target) ref() string {
	if t.card.Ref != "" {
		return t.card.Ref
	}
	return t.project + "#" + strconv.FormatInt(t.card.Number, 10)
}

func (t target) path() string { return cardPath(t.project, t.card.Number) }

func (t target) url() string {
	return fmt.Sprintf("%s/projects/%s/issues/%d", t.agent.linkBase, t.project, t.card.Number)
}

// linked returns the agent a chat reaches a project through.
func (m *Module) linked(ctx context.Context, to otogi.Destination, project string) (*agent, error) {
	links, err := m.store.links(ctx, &to)
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(links, func(l link) bool { return l.Project == project })
	if i < 0 {
		return nil, refuse("%s is not linked to this chat.", project)
	}
	a := m.agents[links[i].Agent]
	if a == nil {
		return nil, refuse("%s is linked through %s, which is no longer configured.", project, links[i].Agent)
	}
	return a, nil
}

var (
	cardURL   = regexp.MustCompile(`/projects/([^/]+)/issues/(\d+)`)
	slugRef   = regexp.MustCompile(`^([a-z0-9][a-z0-9_-]*)[/#](\d+)$`)
	numberRef = regexp.MustCompile(`^#?(\d+)$`)
)

// resolve finds the card ref names, looking only in the projects linked to
// the chat, so nothing outside them is ever read: "<project>/<n>", a card
// URL, the project's prefix form ("MJT-12"), or a bare number when the chat
// links one project.
func (m *Module) resolve(ctx context.Context, to otogi.Destination, ref string) (target, error) {
	ref = strings.TrimSpace(ref)
	var project string
	var number int64
	if u, err := url.Parse(ref); err == nil && u.Scheme != "" {
		if match := cardURL.FindStringSubmatch(u.Path); match != nil {
			project, number = match[1], atoi(match[2])
		}
	} else if match := slugRef.FindStringSubmatch(ref); match != nil {
		project, number = match[1], atoi(match[2])
	}
	if project != "" {
		a, err := m.linked(ctx, to, project)
		if err != nil {
			return target{}, err
		}
		return m.read(ctx, a, project, cardPath(project, number))
	}
	links, err := m.store.links(ctx, &to)
	if err != nil {
		return target{}, err
	}
	if len(links) == 0 {
		return target{}, refuse("No todou projects are linked to this chat.")
	}
	if match := numberRef.FindStringSubmatch(ref); match != nil {
		if len(links) > 1 {
			return target{}, refuse("This chat links several projects; name the card with its project, like %s/%s.", links[0].Project, match[1])
		}
		a, err := m.linked(ctx, to, links[0].Project)
		if err != nil {
			return target{}, err
		}
		return m.read(ctx, a, links[0].Project, cardPath(links[0].Project, atoi(match[1])))
	}
	// A prefix form: each linked project refuses a prefix that is not its own.
	for _, l := range links {
		a := m.agents[l.Agent]
		if a == nil {
			continue
		}
		if t, err := m.read(ctx, a, l.Project, ref); err == nil {
			return t, nil
		}
	}
	return target{}, refuse("No card %s in the projects linked to this chat.", ref)
}

// read reads a card in a project through an agent.
func (m *Module) read(ctx context.Context, a *agent, project, ref string) (target, error) {
	c, err := a.cli.view(ctx, project, ref)
	if err != nil {
		var cli *cliError
		if errors.As(err, &cli) {
			return target{}, refuse("Could not read %s: %s", ref, cli.stderr)
		}
		return target{}, err
	}
	return target{agent: a, project: project, card: c}, nil
}

func atoi(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

// source is who asked for a write: the person whose message it answers
// and, for a chat agent's tool call, the agent.
type source struct {
	event otogi.Event
	via   string // the chat agent, or "" for a command
}

// line is the source line ending every body and comment written from chat,
// as the todou-agent skill ends a filed card's body.
func (s source) line() string {
	e := s.event
	platform := string(e.Platform)
	if platform != "" {
		platform = strings.ToUpper(platform[:1]) + platform[1:]
	}
	where := "in " + platform
	switch {
	case e.Conversation.Title != "":
		where += " “" + e.Conversation.Title + "”"
	case e.Conversation.Kind == otogi.ConversationDirect:
		where = "in a private " + platform + " chat"
	}
	line := "— " + speaker(e.Actor) + ", " + where
	if s.via != "" {
		line += " via " + s.via
	}
	return line
}

func speaker(a otogi.Actor) string {
	if a.Name != "" {
		return a.Name
	}
	return string(a.ID)
}

func (s source) path() string {
	if s.via != "" {
		return "tool:" + s.via
	}
	return "command"
}

// signed is a body with the source line after it.
func (s source) signed(body string) string {
	if body = strings.TrimSpace(body); body != "" {
		return body + "\n\n" + s.line()
	}
	return s.line()
}

// write runs a write as the agent and records it as todou.write, with who
// asked, the arguments, and the outcome. A CLI failure becomes a refusal
// carrying todou's own message, so the person sees why.
func (m *Module) write(ctx context.Context, src source, a *agent, project, op string, args map[string]any, do func() (any, error)) (any, error) {
	result, err := do()
	payload := map[string]any{"agent": a.name, "project": project, "op": op, "args": args, "by": src.event.ActorKey(), "path": src.path(), "result": result}
	outcome := "ok"
	if err != nil {
		outcome, payload["error"] = "error", err.Error()
	}
	e := src.event
	m.host.Record(ctx, otogi.Record{Kind: "todou.write", Event: e.ID, Platform: e.Platform, Account: e.Account, Conversation: e.Conversation.ID, Outcome: outcome, Payload: payload})
	var cli *cliError
	if errors.As(err, &cli) {
		return nil, refuse("todou refused: %s", cli.stderr)
	}
	return result, err
}

// created is the outcome of filing a card.
type created struct {
	Ref string `json:"ref"`
	URL string `json:"url"`
}

// create files a card in a linked project. Labels must exist already: the
// CLI would create unknown ones on the spot.
func (m *Module) create(ctx context.Context, src source, project, title, body, status string, labels, assignees []string) (created, error) {
	a, err := m.linked(ctx, src.event.Destination(), project)
	if err != nil {
		return created{}, err
	}
	if strings.TrimSpace(title) == "" {
		return created{}, refuse("A card needs a title.")
	}
	if len(labels) > 0 {
		f, err := a.cli.facts(ctx, project)
		if err != nil {
			return created{}, err
		}
		for _, l := range labels {
			if !slices.Contains(f.Labels, l) {
				return created{}, refuse("%s has no label %q; its labels are: %s.", project, l, strings.Join(f.Labels, ", "))
			}
		}
	}
	args := map[string]any{"title": title, "body": src.signed(body), "status": status, "labels": labels, "assignees": assignees}
	result, err := m.write(ctx, src, a, project, "create", args, func() (any, error) {
		c, err := a.cli.create(ctx, project, title, src.signed(body), status, labels, assignees)
		if err != nil {
			return nil, err
		}
		t := target{agent: a, project: project, card: c}
		return created{Ref: t.ref(), URL: t.url()}, nil
	})
	if err != nil {
		return created{}, err
	}
	return result.(created), nil
}

// commentOn adds a comment to a card and returns its permalink.
func (m *Module) commentOn(ctx context.Context, src source, t target, body string) (string, error) {
	if strings.TrimSpace(body) == "" {
		return "", refuse("The comment is empty.")
	}
	result, err := m.write(ctx, src, t.agent, t.project, "comment", map[string]any{"card": t.path(), "body": src.signed(body)}, func() (any, error) {
		id, err := t.agent.cli.comment(ctx, t.project, t.card.Number, src.signed(body))
		if err != nil {
			return nil, err
		}
		return fmt.Sprintf("%s#comment-%d", t.url(), id), nil
	})
	if err != nil {
		return "", err
	}
	return result.(string), nil
}

func (m *Module) move(ctx context.Context, src source, t target, status string) error {
	_, err := m.write(ctx, src, t.agent, t.project, "move", map[string]any{"card": t.path(), "status": status}, func() (any, error) {
		return nil, t.agent.cli.move(ctx, t.project, t.card.Number, status)
	})
	return err
}

func (m *Module) assign(ctx context.Context, src source, t target, add, remove []string) error {
	if len(add) == 0 && len(remove) == 0 {
		return refuse("Name someone to assign or unassign.")
	}
	_, err := m.write(ctx, src, t.agent, t.project, "assign", map[string]any{"card": t.path(), "add": add, "remove": remove}, func() (any, error) {
		return nil, t.agent.cli.assign(ctx, t.project, t.card.Number, add, remove)
	})
	return err
}

// block records that t waits for the cards in by, and that the cards in
// blocks wait for t, or with unblock removes those edges. The other cards
// must be in projects linked to the chat too.
func (m *Module) block(ctx context.Context, src source, t target, by, blocks []target, unblock bool) error {
	if len(by) == 0 && len(blocks) == 0 {
		return refuse("Name the cards it is blocked by or blocks.")
	}
	paths := func(ts []target) []string {
		out := make([]string, len(ts))
		for i, o := range ts {
			out[i] = o.path()
		}
		return out
	}
	op := "block"
	if unblock {
		op = "unblock"
	}
	_, err := m.write(ctx, src, t.agent, t.project, op, map[string]any{"card": t.path(), "by": paths(by), "blocks": paths(blocks)}, func() (any, error) {
		return nil, t.agent.cli.block(ctx, t.project, t.card.Number, paths(by), paths(blocks), unblock)
	})
	return err
}

// summary describes a card in a few lines.
func (t target) summary() string {
	c := t.card
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s\nStatus: %s", t.ref(), c.Title, c.Status.Name)
	if len(c.Assignees) > 0 {
		names := make([]string, len(c.Assignees))
		for i, a := range c.Assignees {
			names[i] = "@" + a.Login
		}
		b.WriteString(" · " + strings.Join(names, ", "))
	}
	if len(c.Labels) > 0 {
		names := make([]string, len(c.Labels))
		for i, l := range c.Labels {
			names[i] = l.Name
		}
		b.WriteString(" · " + strings.Join(names, ", "))
	}
	for _, side := range []struct {
		name  string
		edges []edge
	}{{"Blocked by", c.BlockedBy}, {"Blocks", c.Blocks}} {
		var refs []string
		for _, e := range side.edges {
			if e.ClearedAt == nil {
				refs = append(refs, e.Ref)
			}
		}
		if len(refs) > 0 {
			fmt.Fprintf(&b, "\n%s: %s", side.name, strings.Join(refs, ", "))
		}
	}
	b.WriteString("\n" + t.url())
	return b.String()
}

// commandMarkdown is the message as Markdown after its first skip words
// (the command and its arguments), keeping the formatting and line breaks
// of what follows.
func commandMarkdown(e otogi.Event, skip int) string {
	var parts []string
	for _, p := range e.Message.Parts {
		if p.Kind == otogi.PartText {
			parts = append(parts, markdown.Format(p.Text, p.Annotations))
		}
	}
	rest := strings.Join(parts, "\n\n")
	for range skip {
		rest = strings.TrimLeftFunc(rest, unicode.IsSpace)
		if i := strings.IndexFunc(rest, unicode.IsSpace); i >= 0 {
			rest = rest[i:]
		} else {
			rest = ""
		}
	}
	return strings.TrimLeft(strings.TrimLeftFunc(rest, func(r rune) bool { return r == ' ' || r == '\t' }), "\n")
}

// logins are assignee names, without the @ people write.
func logins(words []string) []string {
	out := make([]string, len(words))
	for i, w := range words {
		out[i] = strings.TrimPrefix(w, "@")
	}
	return out
}

// work answers the commands that read and write cards. Anyone in the chat
// may use them on the projects linked to it.
func (m *Module) work(ctx context.Context, e otogi.Event, verb, arg string) (string, error) {
	src := source{event: e}
	words := strings.Fields(arg)
	to := e.Destination()
	usage := map[string]string{
		"view": "/todou view <card>", "new": "/todou new <project> <title>, then the body on the next lines",
		"comment": "/todou comment <card> <text>", "move": "/todou move <card> <status>",
		"assign": "/todou assign <card> <login>…", "unassign": "/todou unassign <card> <login>…",
		"block": "/todou block <card> by|blocks <card>…", "unblock": "/todou unblock <card> by|blocks <card>…",
	}[verb]
	need := map[string]int{"view": 1, "new": 2, "comment": 2, "move": 2, "assign": 2, "unassign": 2, "block": 3, "unblock": 3}[verb]
	if len(words) < need {
		return "Usage: " + usage, nil
	}
	if verb == "new" {
		first := strings.Fields(strings.SplitN(arg, "\n", 2)[0])
		if len(first) < 2 {
			return "Usage: " + usage, nil
		}
		title := strings.Join(first[1:], " ")
		body := ""
		if md := commandMarkdown(e, 3); strings.Contains(md, "\n") {
			body = md[strings.Index(md, "\n")+1:]
		}
		c, err := m.create(ctx, src, words[0], title, body, "", nil, nil)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Filed %s: %s\n%s", c.Ref, title, c.URL), nil
	}
	t, err := m.resolve(ctx, to, words[0])
	if err != nil {
		return "", err
	}
	switch verb {
	case "view":
		return t.summary(), nil
	case "comment":
		link, err := m.commentOn(ctx, src, t, commandMarkdown(e, 3))
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Commented on %s.\n%s", t.ref(), link), nil
	case "move":
		status := strings.Join(words[1:], " ")
		if err := m.move(ctx, src, t, status); err != nil {
			return "", err
		}
		return fmt.Sprintf("Moved %s to %s.", t.ref(), status), nil
	case "assign", "unassign":
		names := logins(words[1:])
		add, remove := names, []string(nil)
		if verb == "unassign" {
			add, remove = nil, names
		}
		if err := m.assign(ctx, src, t, add, remove); err != nil {
			return "", err
		}
		if verb == "assign" {
			return fmt.Sprintf("Assigned %s to %s.", "@"+strings.Join(names, ", @"), t.ref()), nil
		}
		return fmt.Sprintf("Unassigned %s from %s.", "@"+strings.Join(names, ", @"), t.ref()), nil
	default: // block, unblock
		side := words[1]
		if side != "by" && side != "blocks" {
			return "Usage: " + usage, nil
		}
		var others []target
		var refs []string
		for _, w := range words[2:] {
			for _, ref := range strings.Split(w, ",") {
				if ref = strings.TrimSpace(ref); ref == "" {
					continue
				}
				o, err := m.resolve(ctx, to, ref)
				if err != nil {
					return "", err
				}
				others, refs = append(others, o), append(refs, o.ref())
			}
		}
		by, blocks := others, []target(nil)
		if side == "blocks" {
			by, blocks = nil, others
		}
		if err := m.block(ctx, src, t, by, blocks, verb == "unblock"); err != nil {
			return "", err
		}
		list := strings.Join(refs, ", ")
		switch {
		case verb == "block" && side == "by":
			return fmt.Sprintf("%s is now blocked by %s.", t.ref(), list), nil
		case verb == "block":
			return fmt.Sprintf("%s now blocks %s.", t.ref(), list), nil
		case side == "by":
			return fmt.Sprintf("%s is no longer blocked by %s.", t.ref(), list), nil
		default:
			return fmt.Sprintf("%s no longer blocks %s.", t.ref(), list), nil
		}
	}
}

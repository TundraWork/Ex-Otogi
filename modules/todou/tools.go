package todou

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/llm"
	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// tools are the functions chat agents get with `tools: [todou]`. They work
// only on the projects linked to the chat the agent answers in, as the
// link's agent account, and every write ends with a source line naming who
// asked. The descriptions carry the todou-agent skill's filing rules.
func (m *Module) tools() []llm.Tool {
	cardParam := `"card": {"type": "string", "description": "the card: project/number (mojito/12), the prefix form (MJT-12), its URL, or a bare number when the chat links one project"}`
	return []llm.Tool{
		m.tool("todou_projects", "List the todou projects linked to this chat, with their statuses, labels, and members. Read this before filing or moving cards, to use the project's own names.",
			`{"type": "object", "properties": {}}`,
			func(ctx context.Context, src source, _ json.RawMessage) (any, error) {
				to := src.event.Destination()
				links, err := m.store.links(ctx, &to)
				if err != nil {
					return nil, err
				}
				if len(links) == 0 {
					return nil, refuse(noLinks)
				}
				var out []map[string]any
				for _, l := range links {
					a := m.agents[l.Agent]
					if a == nil {
						continue
					}
					f, err := a.cli.facts(ctx, l.Project)
					if err != nil {
						return nil, err
					}
					out = append(out, map[string]any{"project": l.Project, "agent": l.Agent, "statuses": f.Statuses, "closed_statuses": f.Closed, "labels": f.Labels, "members": f.Members})
				}
				return out, nil
			}),
		m.tool("todou_search", "Search cards, comments, and specs in the projects linked to this chat. Every term must match (case-insensitive substrings); qualifiers like state:open, label:kind:bug, assignee:alice work. Search before filing a card, and reuse an existing card for the same request.",
			`{"type": "object", "properties": {"terms": {"type": "string"}, "project": {"type": "string", "description": "one linked project; default: all of them"}}, "required": ["terms"]}`,
			func(ctx context.Context, src source, raw json.RawMessage) (any, error) {
				var in struct{ Terms, Project string }
				if err := json.Unmarshal(raw, &in); err != nil {
					return nil, err
				}
				to := src.event.Destination()
				links, err := m.store.links(ctx, &to)
				if err != nil {
					return nil, err
				}
				if len(links) == 0 {
					return nil, refuse(noLinks)
				}
				var out []map[string]string
				for _, l := range links {
					a := m.agents[l.Agent]
					if a == nil || in.Project != "" && in.Project != l.Project {
						continue
					}
					hits, err := a.cli.search(ctx, l.Project, strings.Fields(in.Terms), 10)
					if err != nil {
						return nil, err
					}
					for _, h := range hits {
						out = append(out, map[string]string{"card": h.Issue.Ref, "title": h.Issue.Title, "status": h.Issue.Status.Name, "matched": h.Field, "snippet": h.Snippet.Text})
					}
				}
				return out, nil
			}),
		m.tool("todou_view", "Read a card: title, status, labels, assignees, blocks, body, and its last comments. Reading does not mark the card read.",
			`{"type": "object", "properties": {`+cardParam+`}, "required": ["card"]}`,
			func(ctx context.Context, src source, raw json.RawMessage) (any, error) {
				t, err := m.toolTarget(ctx, src, raw)
				if err != nil {
					return nil, err
				}
				comments, err := t.agent.cli.comments(ctx, t.project, t.card.Number, 10)
				if err != nil {
					return nil, err
				}
				var recent []map[string]string
				for _, c := range comments {
					if c.HiddenAt != nil {
						continue
					}
					recent = append(recent, map[string]string{"author": c.Author.name(), "at": c.CreatedAt.Format(time.RFC3339), "body": c.Body})
				}
				return map[string]any{"card": t.ref(), "url": t.url(), "summary": t.summary(), "body": t.card.Body, "comments": recent}, nil
			}),
		m.tool("todou_create", "File a new card in a linked project. Search first. The title names the reported symptom, never a guessed cause. The body holds the reporter's own words and the evidence they gave, as written; put your own interpretation in a comment afterwards (todou_comment). Unrelated asks become separate cards. Use only the project's existing labels. Leave the card unassigned unless the person asked to assign it. A line saying who asked is added for you.",
			`{"type": "object", "properties": {
				"project": {"type": "string"}, "title": {"type": "string"}, "body": {"type": "string", "description": "Markdown"},
				"status": {"type": "string", "description": "the project's status name; default: its default status"},
				"labels": {"type": "array", "items": {"type": "string"}}, "assignees": {"type": "array", "items": {"type": "string"}, "description": "logins, only if the person asked"}},
				"required": ["project", "title", "body"]}`,
			func(ctx context.Context, src source, raw json.RawMessage) (any, error) {
				var in struct {
					Project, Title, Body, Status string
					Labels, Assignees            []string
				}
				if err := json.Unmarshal(raw, &in); err != nil {
					return nil, err
				}
				return m.create(ctx, src, in.Project, in.Title, in.Body, in.Status, in.Labels, logins(in.Assignees))
			}),
		m.tool("todou_comment", "Comment on a card. Lead with the point, one point per comment; for a person's request, keep their words. A line saying who asked is added for you.",
			`{"type": "object", "properties": {`+cardParam+`, "body": {"type": "string", "description": "Markdown"}}, "required": ["card", "body"]}`,
			func(ctx context.Context, src source, raw json.RawMessage) (any, error) {
				var in struct{ Body string }
				if err := json.Unmarshal(raw, &in); err != nil {
					return nil, err
				}
				t, err := m.toolTarget(ctx, src, raw)
				if err != nil {
					return nil, err
				}
				link, err := m.commentOn(ctx, src, t, in.Body)
				return map[string]string{"comment": link}, err
			}),
		m.tool("todou_move", "Move a card to one of its project's statuses; closing and reopening are moves to a closed or open status. Do it only when a person asks.",
			`{"type": "object", "properties": {`+cardParam+`, "status": {"type": "string"}}, "required": ["card", "status"]}`,
			func(ctx context.Context, src source, raw json.RawMessage) (any, error) {
				var in struct{ Status string }
				if err := json.Unmarshal(raw, &in); err != nil {
					return nil, err
				}
				t, err := m.toolTarget(ctx, src, raw)
				if err != nil {
					return nil, err
				}
				return map[string]string{"moved": t.ref(), "to": in.Status}, m.move(ctx, src, t, in.Status)
			}),
		m.tool("todou_assign", "Add or remove a card's assignees, by login. The assignee field dispatches work, so change it only when a person explicitly asks.",
			`{"type": "object", "properties": {`+cardParam+`, "add": {"type": "array", "items": {"type": "string"}}, "remove": {"type": "array", "items": {"type": "string"}}}, "required": ["card"]}`,
			func(ctx context.Context, src source, raw json.RawMessage) (any, error) {
				var in struct{ Add, Remove []string }
				if err := json.Unmarshal(raw, &in); err != nil {
					return nil, err
				}
				t, err := m.toolTarget(ctx, src, raw)
				if err != nil {
					return nil, err
				}
				return map[string]any{"card": t.ref(), "added": in.Add, "removed": in.Remove}, m.assign(ctx, src, t, logins(in.Add), logins(in.Remove))
			}),
		m.blockTool("todou_block", "Record that a card waits for other cards (blocked_by), or that other cards wait for it (blocks). Blocks are recorded facts and prevent nothing. Other relationships (duplicate, related, parent and child) are written as comments that link the cards.", false),
		m.blockTool("todou_unblock", "Remove block edges recorded with todou_block.", true),
	}
}

func (m *Module) blockTool(name, description string, unblock bool) llm.Tool {
	return m.tool(name, description,
		`{"type": "object", "properties": {"card": {"type": "string"}, "blocked_by": {"type": "array", "items": {"type": "string"}}, "blocks": {"type": "array", "items": {"type": "string"}}}, "required": ["card"]}`,
		func(ctx context.Context, src source, raw json.RawMessage) (any, error) {
			var in struct {
				BlockedBy []string `json:"blocked_by"`
				Blocks    []string `json:"blocks"`
			}
			if err := json.Unmarshal(raw, &in); err != nil {
				return nil, err
			}
			t, err := m.toolTarget(ctx, src, raw)
			if err != nil {
				return nil, err
			}
			targets := func(refs []string) ([]target, error) {
				var out []target
				for _, r := range refs {
					o, err := m.resolve(ctx, src.event.Destination(), r)
					if err != nil {
						return nil, err
					}
					out = append(out, o)
				}
				return out, nil
			}
			by, err := targets(in.BlockedBy)
			if err != nil {
				return nil, err
			}
			blocks, err := targets(in.Blocks)
			if err != nil {
				return nil, err
			}
			return map[string]any{"card": t.ref(), "blocked_by": in.BlockedBy, "blocks": in.Blocks}, m.block(ctx, src, t, by, blocks, unblock)
		})
}

// tool makes a todou tool. A refusal reaches the model as the tool's error,
// in plain words it can pass on.
func (m *Module) tool(name, description, parameters string, call func(context.Context, source, json.RawMessage) (any, error)) llm.Tool {
	return llm.NewTool(llm.Function{Name: name, Description: description, Parameters: json.RawMessage(parameters)},
		func(ctx context.Context, e otogi.Event, args json.RawMessage) (any, error) {
			out, err := call(ctx, source{event: e, via: llm.AgentFrom(ctx)}, args)
			var r *refusal
			if errors.As(err, &r) {
				return nil, errors.New(r.msg)
			}
			return out, err
		})
}

// toolTarget resolves a tool call's "card" in the chat's linked projects.
func (m *Module) toolTarget(ctx context.Context, src source, raw json.RawMessage) (target, error) {
	var in struct{ Card string }
	if err := json.Unmarshal(raw, &in); err != nil {
		return target{}, fmt.Errorf("arguments: %w", err)
	}
	return m.resolve(ctx, src.event.Destination(), in.Card)
}

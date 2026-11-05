package chat

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// answers reports whether p answers in chat: as /chat last set it there,
// or else as the config lists it.
func (m *Module) answers(p *profile, chat string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if allowed, ok := m.access[p.Name][chat]; ok {
		return allowed
	}
	return p.chats[chat]
}

// command runs /chat: list shows which agents answer in a chat, and allow
// and deny, for operators, change it. The chat defaults to this one; another
// is named as /whoami prints it.
func (m *Module) command(ctx context.Context, e otogi.Event, inv otogi.Invocation) error {
	argv := e.Command.Argv
	verb := "list"
	if len(argv) > 0 {
		verb, argv = argv[0], argv[1:]
	}
	var reply string
	var err error
	switch verb {
	case "list":
		reply, err = m.list(e, argv)
	case "allow", "deny":
		reply, err = m.change(ctx, e, verb == "allow", argv)
	default:
		reply = "Usage: /chat list [chat] | allow <agent> [chat] | deny <agent> [chat]"
	}
	if err != nil {
		return err
	}
	_, err = inv.Send(ctx, "reply", otogi.Reply(reply, e.Ref()))
	return err
}

func (m *Module) list(e otogi.Event, argv []string) (string, error) {
	chat, problem := chatArg(e, argv)
	if problem != "" {
		return problem, nil
	}
	var names []string
	for _, p := range m.profiles {
		if m.answers(p, chat) {
			names = append(names, p.Name)
		}
	}
	if len(names) == 0 {
		return "No agent answers in " + chat + ".", nil
	}
	return "In " + chat + ": " + strings.Join(names, ", ") + ".", nil
}

func (m *Module) change(ctx context.Context, e otogi.Event, allow bool, argv []string) (string, error) {
	if e.Actor.ID == "" || !slices.Contains(m.operators, e.ActorKey()) {
		return fmt.Sprintf("Only operators can change where agents answer. Your ID is %s.", e.ActorKey()), nil
	}
	if len(argv) == 0 {
		return "Name the agent: /chat allow <agent> [chat].", nil
	}
	p := m.profileNamed(argv[0])
	if p == nil {
		var names []string
		for _, p := range m.profiles {
			names = append(names, p.Name)
		}
		return fmt.Sprintf("No agent %q; the agents are %s.", argv[0], strings.Join(names, ", ")), nil
	}
	chat, problem := chatArg(e, argv[1:])
	if problem != "" {
		return problem, nil
	}
	if m.answers(p, chat) == allow {
		if allow {
			return p.Name + " already answers in " + chat + ".", nil
		}
		return p.Name + " does not answer in " + chat + ".", nil
	}
	if err := m.store.setAccess(ctx, p.Name, chat, allow, e.ActorKey(), m.now()); err != nil {
		return "", err
	}
	m.mu.Lock()
	if m.access[p.Name] == nil {
		m.access[p.Name] = map[string]bool{}
	}
	m.access[p.Name][chat] = allow
	m.mu.Unlock()
	m.host.Record(ctx, otogi.Record{Kind: "chat.access", Outcome: "ok", Payload: map[string]any{"profile": p.Name, "chat": chat, "allowed": allow, "by": e.ActorKey()}})
	if allow {
		return p.Name + " now answers in " + chat + ".", nil
	}
	return p.Name + " no longer answers in " + chat + ".", nil
}

// profileNamed finds an agent by its name or one of its keywords.
func (m *Module) profileNamed(name string) *profile {
	for _, p := range m.profiles {
		if strings.EqualFold(p.Name, name) || slices.ContainsFunc(p.keywords, func(k string) bool { return strings.EqualFold(k, name) }) {
			return p
		}
	}
	return nil
}

// chatArg is the chat a command names, or the one it was sent in.
func chatArg(e otogi.Event, argv []string) (chat, problem string) {
	switch {
	case len(argv) == 0:
		return chatKey(e), ""
	case len(argv) > 1:
		return "", "Name one chat, as /whoami prints it."
	}
	if platform, id, ok := strings.Cut(argv[0], ":"); !ok || platform == "" || id == "" {
		return "", fmt.Sprintf("%q is not a chat; name it as /whoami prints it, like %s.", argv[0], chatKey(e))
	}
	return argv[0], ""
}

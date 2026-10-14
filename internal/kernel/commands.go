package kernel

import (
	"context"
	"regexp"
	"strings"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

// commandName is the accepted syntax of command names.
var commandName = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)

// CommandParser returns middleware that sets Event.Command on new messages
// whose text starts with one of prefixes, such as "/remind 10m tea" or
// "/remind@mybot 10m tea". Events that already carry a command are left alone.
func CommandParser(prefixes []string) otogi.Middleware {
	return commandParser{prefixes: prefixes}
}

type commandParser struct{ prefixes []string }

func (commandParser) ID() otogi.MiddlewareID { return "commands" }

func (p commandParser) Intercept(_ context.Context, e *otogi.Event) (otogi.Verdict, error) {
	if e.Command != nil || e.Action != otogi.ActionMessageCreated {
		return otogi.Continue, nil
	}
	text := strings.TrimSpace(e.Message.PlainText())
	for _, prefix := range p.prefixes {
		rest, ok := strings.CutPrefix(text, prefix)
		if !ok {
			continue
		}
		head, args, _ := strings.Cut(rest, " ")
		name, _, _ := strings.Cut(head, "@") // "/cmd@bot" addresses one bot in a group
		name = strings.ToLower(name)
		if !commandName.MatchString(name) {
			return otogi.Continue, nil
		}
		args = strings.TrimSpace(args)
		e.Command = &otogi.Command{Name: name, Args: args, Argv: strings.Fields(args)}
		return otogi.Continue, nil
	}
	return otogi.Continue, nil
}

package memory

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"ex-otogi/pkg/otogi/core"
	"ex-otogi/pkg/otogi/platform"
)

const (
	rawCommandName         = "raw"
	historyCommandName     = "history"
	maxCommandReplyLength  = 3950
	maxCommandArgumentSize = 1
)

type introspectionCommandKind string

const (
	introspectionCommandKindRaw     introspectionCommandKind = "raw"
	introspectionCommandKindHistory introspectionCommandKind = "history"
)

type introspectionCommand struct {
	kind      introspectionCommandKind
	articleID string
}

func commandReplyEntities(body string) []platform.TextEntity {
	if body == "" {
		return nil
	}

	return []platform.TextEntity{
		{
			Type:      platform.TextEntityTypeBlockquote,
			Offset:    0,
			Length:    utf8.RuneCountInString(body),
			Collapsed: true,
		},
	}
}

func parseIntrospectionCommand(commandInvocation *platform.CommandInvocation) (introspectionCommand, error) {
	command := introspectionCommand{}
	if commandInvocation == nil {
		return command, fmt.Errorf("missing command invocation")
	}

	name, matched := matchCommandName(commandInvocation.Name)
	if !matched {
		return command, fmt.Errorf("unsupported introspection command %q", commandInvocation.Name)
	}
	command.kind = name

	fields := strings.Fields(strings.TrimSpace(commandInvocation.Value))
	if len(fields) > maxCommandArgumentSize {
		return command, fmt.Errorf("%s: expected at most one integer article id argument", command.kind)
	}

	if len(fields) == 1 {
		articleID, err := parseArticleIDArgument(fields[0])
		if err != nil {
			return command, fmt.Errorf("%s: %w", command.kind, err)
		}
		command.articleID = articleID
	}

	return command, nil
}

func matchCommandName(name string) (introspectionCommandKind, bool) {
	command := strings.ToLower(strings.TrimSpace(name))
	if command == "" {
		return "", false
	}

	switch command {
	case rawCommandName:
		return introspectionCommandKindRaw, true
	case historyCommandName:
		return introspectionCommandKindHistory, true
	default:
		return "", false
	}
}

func parseArticleIDArgument(argument string) (string, error) {
	articleID, err := strconv.ParseInt(argument, 10, 64)
	if err != nil || articleID <= 0 {
		return "", fmt.Errorf("invalid article id %q, expected a positive integer", argument)
	}

	return strconv.FormatInt(articleID, 10), nil
}

func commandLookup(event *platform.Event, explicitArticleID string) (core.MemoryLookup, bool, error) {
	if event == nil {
		return core.MemoryLookup{}, false, fmt.Errorf("nil event")
	}

	if explicitArticleID != "" {
		lookup := core.MemoryLookup{
			TenantID:       event.TenantID,
			Platform:       event.Source.Platform,
			ConversationID: event.Conversation.ID,
			ArticleID:      explicitArticleID,
		}
		if err := lookup.Validate(); err != nil {
			return core.MemoryLookup{}, false, fmt.Errorf("explicit article id lookup: %w", err)
		}

		return lookup, true, nil
	}

	if event.Article == nil || event.Article.ReplyToArticleID == "" {
		return core.MemoryLookup{}, false, nil
	}

	lookup, err := core.ReplyMemoryLookupFromEvent(event)
	if err != nil {
		return core.MemoryLookup{}, false, fmt.Errorf("reply lookup: %w", err)
	}

	return lookup, true, nil
}

func notFoundMessage(kind introspectionCommandKind, explicitLookup bool) string {
	if explicitLookup {
		return fmt.Sprintf("%s: article not found in memory", kind)
	}

	return fmt.Sprintf("%s: replied article not found in memory", kind)
}

func trimForCommandReply(body string) string {
	runes := 0
	for index := range body {
		if runes == maxCommandReplyLength {
			return body[:index] + "\n...(truncated)"
		}
		runes++
	}

	return body
}

func formatRawEntity(cached memorySnapshot) (string, error) {
	body, err := json.MarshalIndent(cached, "", "  ")
	if err != nil {
		return "", fmt.Errorf("format raw entity json: %w", err)
	}

	return string(body), nil
}

func formatHistoryEvents(events []platform.Event) (string, error) {
	body, err := json.MarshalIndent(events, "", "  ")
	if err != nil {
		return "", fmt.Errorf("format history events json: %w", err)
	}

	return string(body), nil
}

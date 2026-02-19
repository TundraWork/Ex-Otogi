package memory

import (
	"context"
	"fmt"

	"ex-otogi/pkg/otogi"
)

func (m *Module) handleEvent(ctx context.Context, event *otogi.Event) error {
	if event == nil {
		return fmt.Errorf("memory handle event: nil event")
	}

	switch event.Kind {
	case otogi.EventKindArticleCreated:
		if err := m.appendEvent(event); err != nil {
			return fmt.Errorf("memory handle %s append event: %w", event.Kind, err)
		}
		if err := m.rememberCreated(event); err != nil {
			return fmt.Errorf("memory handle %s project article: %w", event.Kind, err)
		}
	case otogi.EventKindArticleEdited:
		if err := m.appendEvent(event); err != nil {
			return fmt.Errorf("memory handle %s append event: %w", event.Kind, err)
		}
		if err := m.rememberEdit(event); err != nil {
			return fmt.Errorf("memory handle %s project article: %w", event.Kind, err)
		}
	case otogi.EventKindArticleRetracted:
		if err := m.appendEvent(event); err != nil {
			return fmt.Errorf("memory handle %s append event: %w", event.Kind, err)
		}
		if err := m.forgetRetracted(event); err != nil {
			return fmt.Errorf("memory handle %s project article: %w", event.Kind, err)
		}
	case otogi.EventKindArticleReactionAdded, otogi.EventKindArticleReactionRemoved:
		if err := m.appendEvent(event); err != nil {
			return fmt.Errorf("memory handle %s append event: %w", event.Kind, err)
		}
		if err := m.rememberReaction(event); err != nil {
			return fmt.Errorf("memory handle %s project article: %w", event.Kind, err)
		}
	case otogi.EventKindSystemCommandReceived:
		if err := m.handleCommandEvent(ctx, event); err != nil {
			return fmt.Errorf("memory handle %s: %w", event.Kind, err)
		}
	default:
	}

	return nil
}

func (m *Module) handleCommandEvent(ctx context.Context, event *otogi.Event) error {
	if event == nil {
		return fmt.Errorf("nil event")
	}
	if event.Kind != otogi.EventKindSystemCommandReceived {
		return nil
	}
	if event.Command == nil {
		return fmt.Errorf("missing command payload")
	}

	command, parseErr := parseIntrospectionCommand(event.Command)
	if parseErr != nil && command.kind == "" {
		return nil
	}
	if m.dispatcher == nil {
		return fmt.Errorf("%s command: outbound dispatcher not configured", command.kind)
	}

	target, err := otogi.OutboundTargetFromEvent(event)
	if err != nil {
		return fmt.Errorf("%s command derive outbound target: %w", command.kind, err)
	}
	if parseErr != nil {
		return m.replyCommandResult(ctx, target, event.Article.ID, parseErr.Error())
	}

	lookup, hasTarget, err := commandLookup(event, command.articleID)
	if err != nil {
		return fmt.Errorf("%s command resolve target lookup: %w", command.kind, err)
	}
	if !hasTarget {
		return nil
	}

	var body string
	switch command.kind {
	case introspectionCommandKindRaw:
		entity, found, getErr := m.getSnapshot(ctx, lookup)
		if getErr != nil {
			return fmt.Errorf("raw command resolve article projection: %w", getErr)
		}
		if !found {
			return m.replyCommandResult(
				ctx,
				target,
				event.Article.ID,
				notFoundMessage(command.kind, command.articleID != ""),
			)
		}

		formattedBody, formatErr := formatRawEntity(entity)
		if formatErr != nil {
			return fmt.Errorf("raw command format entity: %w", formatErr)
		}
		body = formattedBody
	case introspectionCommandKindHistory:
		history, found, getErr := m.getHistory(ctx, lookup)
		if getErr != nil {
			return fmt.Errorf("history command resolve event history: %w", getErr)
		}
		if !found {
			return m.replyCommandResult(
				ctx,
				target,
				event.Article.ID,
				notFoundMessage(command.kind, command.articleID != ""),
			)
		}

		formattedBody, formatErr := formatHistoryEvents(history)
		if formatErr != nil {
			return fmt.Errorf("history command format history: %w", formatErr)
		}
		body = formattedBody
	default:
		return fmt.Errorf("unsupported introspection command %q", command.kind)
	}

	return m.replyCommandResult(ctx, target, event.Article.ID, trimForCommandReply(body))
}

func (m *Module) replyCommandResult(
	ctx context.Context,
	target otogi.OutboundTarget,
	replyToMessageID string,
	body string,
) error {
	_, err := m.dispatcher.SendMessage(ctx, otogi.SendMessageRequest{
		Target:           target,
		Text:             body,
		Entities:         commandReplyEntities(body),
		ReplyToMessageID: replyToMessageID,
	})
	if err != nil {
		return fmt.Errorf("send introspection command message: %w", err)
	}

	return nil
}

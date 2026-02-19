package kernel

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"ex-otogi/pkg/otogi"
)

type commandRegistration struct {
	moduleName string
	spec       otogi.CommandSpec
}

// registerModuleCommands validates and registers module-owned command specs.
func (k *Kernel) registerModuleCommands(
	_ context.Context,
	moduleName string,
	commands []otogi.CommandSpec,
) error {
	if len(commands) == 0 {
		return nil
	}

	normalized := make([]otogi.CommandSpec, 0, len(commands))
	seenInModule := make(map[string]struct{}, len(commands))
	for index, command := range commands {
		if err := command.Validate(); err != nil {
			return fmt.Errorf("register command[%d] for module %s: %w", index, moduleName, err)
		}

		command = cloneCommandSpec(command)
		key := commandRegistryKey(command.Prefix, command.Name)
		if _, exists := seenInModule[key]; exists {
			return fmt.Errorf(
				"register command %s for module %s: duplicate declaration",
				formatCommandKey(command.Prefix, command.Name),
				moduleName,
			)
		}
		seenInModule[key] = struct{}{}
		normalized = append(normalized, command)
	}

	k.mu.Lock()
	defer k.mu.Unlock()
	for _, command := range normalized {
		key := commandRegistryKey(command.Prefix, command.Name)
		existing, exists := k.commands[key]
		if exists {
			return fmt.Errorf(
				"register command %s for module %s: already registered by module %s",
				formatCommandKey(command.Prefix, command.Name),
				moduleName,
				existing.moduleName,
			)
		}
	}
	for _, command := range normalized {
		key := commandRegistryKey(command.Prefix, command.Name)
		k.commands[key] = commandRegistration{
			moduleName: moduleName,
			spec:       command,
		}
	}

	return nil
}

// unregisterModuleCommands removes every command owned by one module.
func (k *Kernel) unregisterModuleCommands(moduleName string) {
	k.mu.Lock()
	defer k.mu.Unlock()

	for key, registration := range k.commands {
		if registration.moduleName == moduleName {
			delete(k.commands, key)
		}
	}
}

// lookupCommand resolves one command spec by prefix + normalized name.
func (k *Kernel) lookupCommand(prefix otogi.CommandPrefix, name string) (otogi.CommandSpec, bool) {
	key := commandRegistryKey(prefix, name)

	k.mu.RLock()
	registration, exists := k.commands[key]
	k.mu.RUnlock()
	if !exists {
		return otogi.CommandSpec{}, false
	}

	return cloneCommandSpec(registration.spec), true
}

// newDriverEventSink creates the source-event sink wrapped with command derivation.
func (k *Kernel) newDriverEventSink() otogi.EventSink {
	return &commandDerivingSink{
		base: k.bus,
		lookupCommand: func(prefix otogi.CommandPrefix, name string) (otogi.CommandSpec, bool) {
			return k.lookupCommand(prefix, name)
		},
		serviceLookup: k.services,
		reportAsync:   k.cfg.onAsyncError,
	}
}

// commandDerivingSink publishes source events and derives command events.
type commandDerivingSink struct {
	base          otogi.EventSink
	lookupCommand func(prefix otogi.CommandPrefix, name string) (otogi.CommandSpec, bool)
	serviceLookup otogi.ServiceRegistry
	reportAsync   func(context.Context, string, error)
}

// Publish forwards one source event and conditionally derives one command event.
func (s *commandDerivingSink) Publish(ctx context.Context, event *otogi.Event) error {
	if event == nil {
		return fmt.Errorf("publish command deriving sink: nil event")
	}
	if s.base == nil {
		return fmt.Errorf("publish command deriving sink: nil base sink")
	}

	if err := s.base.Publish(ctx, event); err != nil {
		return fmt.Errorf("publish source event %s: %w", event.Kind, err)
	}

	if !isCommandDerivableEventKind(event.Kind) {
		return nil
	}

	commandText, commandArticle, ok := commandContextFromEvent(event)
	if !ok {
		return nil
	}
	candidate, matched, parseErr := otogi.ParseCommandCandidate(commandText)
	if !matched {
		return nil
	}

	spec, registered := s.lookupCommand(candidate.Prefix, candidate.Name)
	if !registered {
		return nil
	}
	if parseErr != nil {
		s.replyCommandError(ctx, event, spec, parseErr)
		return nil
	}

	invocation, bindErr := otogi.BindCommand(candidate, spec, event)
	if bindErr != nil {
		s.replyCommandError(ctx, event, spec, bindErr)
		return nil
	}

	commandEvent := derivedCommandEvent(event, commandArticle, candidate.Prefix, invocation)
	if err := s.base.Publish(ctx, commandEvent); err != nil {
		return fmt.Errorf("publish derived command %s: %w", invocation.Name, err)
	}

	return nil
}

func (s *commandDerivingSink) replyCommandError(
	ctx context.Context,
	sourceEvent *otogi.Event,
	spec otogi.CommandSpec,
	parseErr error,
) {
	if s.serviceLookup == nil {
		s.reportAsyncError(
			ctx,
			"command error reply resolve dispatcher",
			fmt.Errorf("service lookup unavailable"),
		)
		return
	}

	dispatcher, err := otogi.ResolveAs[otogi.OutboundDispatcher](
		s.serviceLookup,
		otogi.ServiceOutboundDispatcher,
	)
	if err != nil {
		if errors.Is(err, otogi.ErrServiceNotFound) {
			s.reportAsyncError(ctx, "command error reply resolve dispatcher", err)
			return
		}
		s.reportAsyncError(ctx, "command error reply resolve dispatcher", err)
		return
	}

	target, err := otogi.OutboundTargetFromEvent(sourceEvent)
	if err != nil {
		s.reportAsyncError(ctx, "command error reply derive target", err)
		return
	}

	_, err = dispatcher.SendMessage(ctx, otogi.SendMessageRequest{
		Target:           target,
		Text:             formatCommandErrorReply(spec, parseErr),
		ReplyToMessageID: commandReplyToMessageID(sourceEvent),
	})
	if err != nil {
		s.reportAsyncError(ctx, "command error reply send", err)
	}
}

func (s *commandDerivingSink) reportAsyncError(ctx context.Context, scope string, err error) {
	if s.reportAsync != nil {
		s.reportAsync(ctx, scope, err)
	}
}

func isCommandDerivableEventKind(kind otogi.EventKind) bool {
	return kind == otogi.EventKindArticleCreated || kind == otogi.EventKindArticleEdited
}

func commandContextFromEvent(event *otogi.Event) (text string, article otogi.Article, ok bool) {
	if event == nil {
		return "", otogi.Article{}, false
	}

	switch event.Kind {
	case otogi.EventKindArticleCreated:
		if event.Article == nil {
			return "", otogi.Article{}, false
		}
		return event.Article.Text, cloneArticle(*event.Article), true
	case otogi.EventKindArticleEdited:
		if event.Mutation == nil || event.Mutation.After == nil || event.Mutation.TargetArticleID == "" {
			return "", otogi.Article{}, false
		}
		return event.Mutation.After.Text, otogi.Article{
			ID:       event.Mutation.TargetArticleID,
			Text:     event.Mutation.After.Text,
			Entities: append([]otogi.TextEntity(nil), event.Mutation.After.Entities...),
			Media:    cloneMedia(event.Mutation.After.Media),
		}, true
	default:
		return "", otogi.Article{}, false
	}
}

func derivedCommandEvent(
	sourceEvent *otogi.Event,
	article otogi.Article,
	prefix otogi.CommandPrefix,
	invocation otogi.CommandInvocation,
) *otogi.Event {
	kind, suffix := derivedCommandEventKind(prefix)
	commandEvent := &otogi.Event{
		ID:         sourceEvent.ID + suffix,
		Kind:       kind,
		OccurredAt: sourceEvent.OccurredAt,
		Platform:   sourceEvent.Platform,
		TenantID:   sourceEvent.TenantID,
		Conversation: otogi.Conversation{
			ID:    sourceEvent.Conversation.ID,
			Type:  sourceEvent.Conversation.Type,
			Title: sourceEvent.Conversation.Title,
		},
		Actor: otogi.Actor{
			ID:          sourceEvent.Actor.ID,
			Username:    sourceEvent.Actor.Username,
			DisplayName: sourceEvent.Actor.DisplayName,
			IsBot:       sourceEvent.Actor.IsBot,
		},
		Article:  &article,
		Command:  cloneCommandInvocation(invocation),
		Metadata: cloneStringMap(sourceEvent.Metadata),
	}

	return commandEvent
}

func derivedCommandEventKind(prefix otogi.CommandPrefix) (otogi.EventKind, string) {
	switch prefix {
	case otogi.CommandPrefixSystem:
		return otogi.EventKindSystemCommandReceived, "#system-command"
	default:
		return otogi.EventKindCommandReceived, "#command"
	}
}

func commandReplyToMessageID(event *otogi.Event) string {
	if event == nil {
		return ""
	}
	if event.Article != nil && event.Article.ID != "" {
		return event.Article.ID
	}
	if event.Mutation != nil && event.Mutation.TargetArticleID != "" {
		return event.Mutation.TargetArticleID
	}

	return ""
}

func formatCommandErrorReply(spec otogi.CommandSpec, parseErr error) string {
	if parseErr == nil {
		return commandUsage(spec)
	}

	return fmt.Sprintf("%s\nusage: %s", parseErr.Error(), commandUsage(spec))
}

func commandUsage(spec otogi.CommandSpec) string {
	usage := fmt.Sprintf("%s%s", spec.Prefix, normalizeCommandName(spec.Name))
	if len(spec.Options) == 0 {
		return usage
	}

	parts := make([]string, 0, len(spec.Options))
	for _, option := range spec.Options {
		descriptor := commandOptionDescriptor(option)
		if descriptor == "" {
			continue
		}
		if option.Required {
			parts = append(parts, descriptor)
		} else {
			parts = append(parts, "["+descriptor+"]")
		}
	}
	if len(parts) == 0 {
		return usage
	}

	return usage + " " + strings.Join(parts, " ")
}

func commandOptionDescriptor(option otogi.CommandOptionSpec) string {
	name := normalizeCommandName(option.Name)
	alias := normalizeCommandAlias(option.Alias)
	switch {
	case name != "" && alias != "":
		if option.HasValue {
			return fmt.Sprintf("--%s|-%s <value>", name, alias)
		}
		return fmt.Sprintf("--%s|-%s", name, alias)
	case name != "":
		if option.HasValue {
			return fmt.Sprintf("--%s <value>", name)
		}
		return fmt.Sprintf("--%s", name)
	case alias != "":
		if option.HasValue {
			return fmt.Sprintf("-%s <value>", alias)
		}
		return fmt.Sprintf("-%s", alias)
	default:
		return ""
	}
}

func commandRegistryKey(prefix otogi.CommandPrefix, name string) string {
	return fmt.Sprintf("%s:%s", prefix, normalizeCommandName(name))
}

func formatCommandKey(prefix otogi.CommandPrefix, name string) string {
	return fmt.Sprintf("%s%s", prefix, normalizeCommandName(name))
}

func normalizeCommandName(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func normalizeCommandAlias(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func cloneCommandSpec(spec otogi.CommandSpec) otogi.CommandSpec {
	cloned := spec
	cloned.Name = normalizeCommandName(spec.Name)
	if len(spec.Options) == 0 {
		return cloned
	}

	cloned.Options = append([]otogi.CommandOptionSpec(nil), spec.Options...)
	for index := range cloned.Options {
		cloned.Options[index].Name = normalizeCommandName(cloned.Options[index].Name)
		cloned.Options[index].Alias = normalizeCommandAlias(cloned.Options[index].Alias)
	}

	return cloned
}

func cloneCommandInvocation(invocation otogi.CommandInvocation) *otogi.CommandInvocation {
	cloned := invocation
	if len(invocation.Options) > 0 {
		cloned.Options = append([]otogi.CommandOption(nil), invocation.Options...)
	}

	return &cloned
}

func cloneArticle(article otogi.Article) otogi.Article {
	cloned := article
	if len(article.Entities) > 0 {
		cloned.Entities = append([]otogi.TextEntity(nil), article.Entities...)
	}
	if len(article.Media) > 0 {
		cloned.Media = cloneMedia(article.Media)
	}
	if len(article.Reactions) > 0 {
		cloned.Reactions = append([]otogi.ArticleReaction(nil), article.Reactions...)
	}

	return cloned
}

func cloneMedia(media []otogi.MediaAttachment) []otogi.MediaAttachment {
	if len(media) == 0 {
		return nil
	}

	cloned := make([]otogi.MediaAttachment, 0, len(media))
	for _, item := range media {
		copyItem := item
		if item.Preview != nil {
			preview := *item.Preview
			if len(item.Preview.Bytes) > 0 {
				preview.Bytes = append([]byte(nil), item.Preview.Bytes...)
			}
			copyItem.Preview = &preview
		}
		cloned = append(cloned, copyItem)
	}

	return cloned
}

func cloneStringMap(metadata map[string]string) map[string]string {
	if len(metadata) == 0 {
		return nil
	}

	cloned := make(map[string]string, len(metadata))
	for key, value := range metadata {
		cloned[key] = value
	}

	return cloned
}

package kernel

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"ex-otogi/pkg/otogi/core"
	"ex-otogi/pkg/otogi/platform"
)

type commandRegistration struct {
	moduleName string
	spec       platform.CommandSpec
}

// registerModuleCommands validates and registers module-owned command specs.
func (k *Kernel) registerModuleCommands(
	_ context.Context,
	moduleName string,
	commands []platform.CommandSpec,
) error {
	if len(commands) == 0 {
		return nil
	}

	normalized := make([]platform.CommandSpec, 0, len(commands))
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
func (k *Kernel) lookupCommand(prefix platform.CommandPrefix, name string) (platform.CommandSpec, bool) {
	key := commandRegistryKey(prefix, name)

	k.mu.RLock()
	registration, exists := k.commands[key]
	k.mu.RUnlock()
	if !exists {
		return platform.CommandSpec{}, false
	}

	return cloneCommandSpec(registration.spec), true
}

// newDriverEventDispatcher creates the source-event dispatcher wrapped with command derivation.
func (k *Kernel) newDriverEventDispatcher() core.EventDispatcher {
	return &commandDerivingDispatcher{
		base: k.bus,
		lookupCommand: func(prefix platform.CommandPrefix, name string) (platform.CommandSpec, bool) {
			return k.lookupCommand(prefix, name)
		},
		serviceLookup: k.services,
		reportAsync:   k.cfg.onAsyncError,
		allowlist:     k.cfg.allowlist,
	}
}

// ChatAllowlistConfig controls conversation-level event filtering.
//
// When ConversationIDs is non-empty only events from listed conversations
// receive full processing. Events from unlisted conversations are silently
// dropped unless they match a system command listed in BypassCommands.
// An empty ConversationIDs set disables filtering entirely.
type ChatAllowlistConfig struct {
	// ConversationIDs is the set of conversation identifiers permitted for full event processing.
	ConversationIDs map[string]struct{}
	// BypassCommands lists normalized system command names that are processed even in unlisted conversations.
	BypassCommands map[string]struct{}
}

// IsEnabled reports whether the allowlist restricts any conversations.
func (c ChatAllowlistConfig) IsEnabled() bool {
	return len(c.ConversationIDs) > 0
}

// IsConversationAllowed reports whether a conversation receives full event processing.
//
// The check uses a qualified key built from sourceID and conversationID so that
// allowlist entries are scoped to a specific driver instance.
func (c ChatAllowlistConfig) IsConversationAllowed(sourceID string, conversationID string) bool {
	if !c.IsEnabled() {
		return true
	}

	key := platform.QualifiedConversationKey(sourceID, conversationID)
	_, allowed := c.ConversationIDs[key]

	return allowed
}

// IsBypassCommand reports whether a system command name bypasses the allowlist.
func (c ChatAllowlistConfig) IsBypassCommand(name string) bool {
	if len(c.BypassCommands) == 0 {
		return false
	}

	_, bypass := c.BypassCommands[normalizeCommandName(name)]

	return bypass
}

// commandDerivingDispatcher publishes source events and conditionally derives command events.
type commandDerivingDispatcher struct {
	base          core.EventDispatcher
	lookupCommand func(prefix platform.CommandPrefix, name string) (platform.CommandSpec, bool)
	serviceLookup core.ServiceRegistry
	reportAsync   func(context.Context, string, error)
	allowlist     ChatAllowlistConfig
}

// Publish forwards one source event and conditionally derives one command event.
//
// When the chat allowlist is enabled, events from unlisted conversations are
// silently dropped unless the article text matches a registered system command
// whose name appears in the bypass list. For bypass commands only the derived
// command event is published — the source event is suppressed so that modules
// subscribing to article.created do not process non-allowlisted traffic.
func (s *commandDerivingDispatcher) Publish(ctx context.Context, event *platform.Event) error {
	if event == nil {
		return fmt.Errorf("publish command deriving sink: nil event")
	}
	if s.base == nil {
		return fmt.Errorf("publish command deriving sink: nil base sink")
	}

	if !s.allowlist.IsConversationAllowed(event.Source.ID, event.Conversation.ID) {
		return s.publishBypassOnly(ctx, event)
	}

	if err := s.base.Publish(ctx, event); err != nil {
		return fmt.Errorf("publish source event %s: %w", event.Kind, err)
	}

	return s.deriveCommand(ctx, event)
}

// publishBypassOnly handles events from non-allowlisted conversations.
// Only bypass-eligible system commands are derived and published; everything
// else is silently dropped.
func (s *commandDerivingDispatcher) publishBypassOnly(ctx context.Context, event *platform.Event) error {
	if !isCommandDerivableEventKind(event.Kind) {
		return nil
	}

	commandText, commandArticle, ok := commandContextFromEvent(event)
	if !ok {
		return nil
	}
	candidate, matched, parseErr := platform.ParseCommandCandidate(commandText)
	if !matched {
		return nil
	}
	if candidate.Prefix != platform.CommandPrefixSystem {
		return nil
	}
	if !s.allowlist.IsBypassCommand(candidate.Name) {
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

	invocation, bindErr := platform.BindCommand(candidate, spec, event)
	if bindErr != nil {
		s.replyCommandError(ctx, event, spec, bindErr)
		return nil
	}

	commandEvent := derivedCommandEvent(event, commandArticle, candidate.Prefix, invocation)
	if err := s.base.Publish(ctx, commandEvent); err != nil {
		return fmt.Errorf("publish bypass command %s: %w", invocation.Name, err)
	}

	return nil
}

// deriveCommand attempts to derive a command event from the source event.
func (s *commandDerivingDispatcher) deriveCommand(ctx context.Context, event *platform.Event) error {
	if !isCommandDerivableEventKind(event.Kind) {
		return nil
	}

	commandText, commandArticle, ok := commandContextFromEvent(event)
	if !ok {
		return nil
	}
	candidate, matched, parseErr := platform.ParseCommandCandidate(commandText)
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

	invocation, bindErr := platform.BindCommand(candidate, spec, event)
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

func (s *commandDerivingDispatcher) replyCommandError(
	ctx context.Context,
	sourceEvent *platform.Event,
	spec platform.CommandSpec,
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

	dispatcher, err := core.ResolveAs[platform.SinkDispatcher](
		s.serviceLookup,
		platform.ServiceSinkDispatcher,
	)
	if err != nil {
		if errors.Is(err, core.ErrServiceNotFound) {
			s.reportAsyncError(ctx, "command error reply resolve dispatcher", err)
			return
		}
		s.reportAsyncError(ctx, "command error reply resolve dispatcher", err)
		return
	}

	target, err := platform.OutboundTargetFromEvent(sourceEvent)
	if err != nil {
		s.reportAsyncError(ctx, "command error reply derive target", err)
		return
	}

	_, err = dispatcher.SendMessage(ctx, platform.SendMessageRequest{
		Target:           target,
		Text:             formatCommandErrorReply(spec, parseErr),
		ReplyToMessageID: commandReplyToMessageID(sourceEvent),
	})
	if err != nil {
		s.reportAsyncError(ctx, "command error reply send", err)
	}
}

func (s *commandDerivingDispatcher) reportAsyncError(ctx context.Context, scope string, err error) {
	if s.reportAsync != nil {
		s.reportAsync(ctx, scope, err)
	}
}

func isCommandDerivableEventKind(kind platform.EventKind) bool {
	return kind == platform.EventKindArticleCreated
}

func commandContextFromEvent(event *platform.Event) (text string, article platform.Article, ok bool) {
	if event == nil {
		return "", platform.Article{}, false
	}

	switch event.Kind {
	case platform.EventKindArticleCreated:
		if event.Article == nil {
			return "", platform.Article{}, false
		}
		return event.Article.Text, cloneArticle(*event.Article), true
	case platform.EventKindArticleEdited:
		if event.Mutation == nil || event.Mutation.After == nil || event.Mutation.TargetArticleID == "" {
			return "", platform.Article{}, false
		}
		return event.Mutation.After.Text, platform.Article{
			ID:       event.Mutation.TargetArticleID,
			Text:     event.Mutation.After.Text,
			Entities: append([]platform.TextEntity(nil), event.Mutation.After.Entities...),
			Media:    cloneMedia(event.Mutation.After.Media),
		}, true
	default:
		return "", platform.Article{}, false
	}
}

func derivedCommandEvent(
	sourceEvent *platform.Event,
	article platform.Article,
	prefix platform.CommandPrefix,
	invocation platform.CommandInvocation,
) *platform.Event {
	kind, suffix := derivedCommandEventKind(prefix)
	source := sourceEvent.Source
	commandEvent := &platform.Event{
		ID:         sourceEvent.ID + suffix,
		Kind:       kind,
		OccurredAt: sourceEvent.OccurredAt,
		Source:     source,
		TenantID:   sourceEvent.TenantID,
		Conversation: platform.Conversation{
			ID:    sourceEvent.Conversation.ID,
			Type:  sourceEvent.Conversation.Type,
			Title: sourceEvent.Conversation.Title,
		},
		Actor: platform.Actor{
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

func derivedCommandEventKind(prefix platform.CommandPrefix) (platform.EventKind, string) {
	switch prefix {
	case platform.CommandPrefixSystem:
		return platform.EventKindSystemCommandReceived, "#system-command"
	default:
		return platform.EventKindCommandReceived, "#command"
	}
}

func commandReplyToMessageID(event *platform.Event) string {
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

func formatCommandErrorReply(spec platform.CommandSpec, parseErr error) string {
	if parseErr == nil {
		return commandUsage(spec)
	}

	return fmt.Sprintf("%s\nusage: %s", parseErr.Error(), commandUsage(spec))
}

func commandUsage(spec platform.CommandSpec) string {
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

func commandOptionDescriptor(option platform.CommandOptionSpec) string {
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

func commandRegistryKey(prefix platform.CommandPrefix, name string) string {
	return fmt.Sprintf("%s:%s", prefix, normalizeCommandName(name))
}

func formatCommandKey(prefix platform.CommandPrefix, name string) string {
	return fmt.Sprintf("%s%s", prefix, normalizeCommandName(name))
}

func normalizeCommandName(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func normalizeCommandAlias(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func cloneCommandSpec(spec platform.CommandSpec) platform.CommandSpec {
	cloned := spec
	cloned.Name = normalizeCommandName(spec.Name)
	if len(spec.Options) == 0 {
		return cloned
	}

	cloned.Options = append([]platform.CommandOptionSpec(nil), spec.Options...)
	for index := range cloned.Options {
		cloned.Options[index].Name = normalizeCommandName(cloned.Options[index].Name)
		cloned.Options[index].Alias = normalizeCommandAlias(cloned.Options[index].Alias)
	}

	return cloned
}

func cloneCommandInvocation(invocation platform.CommandInvocation) *platform.CommandInvocation {
	cloned := invocation
	if len(invocation.Options) > 0 {
		cloned.Options = append([]platform.CommandOption(nil), invocation.Options...)
	}

	return &cloned
}

func cloneArticle(article platform.Article) platform.Article {
	cloned := article
	if len(article.Entities) > 0 {
		cloned.Entities = append([]platform.TextEntity(nil), article.Entities...)
	}
	if len(article.Media) > 0 {
		cloned.Media = cloneMedia(article.Media)
	}
	if len(article.Reactions) > 0 {
		cloned.Reactions = append([]platform.ArticleReaction(nil), article.Reactions...)
	}

	return cloned
}

func cloneMedia(media []platform.MediaAttachment) []platform.MediaAttachment {
	if len(media) == 0 {
		return nil
	}

	cloned := make([]platform.MediaAttachment, 0, len(media))
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

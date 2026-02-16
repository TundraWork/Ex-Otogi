package eventcache

import (
	"container/list"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"ex-otogi/pkg/otogi"
)

const (
	defaultMaxEntries      = 10000
	defaultTTL             = 24 * time.Hour
	rawCommandName         = "~raw"
	historyCommandName     = "~history"
	maxCommandReplyLength  = 3950
	maxCommandArgumentSize = 1
)

// ServiceLogger is the optional service registry key for structured logging.
const ServiceLogger = "logger"

// Option mutates event cache module configuration.
type Option func(*Module)

// WithLogger injects a logger directly, bypassing service lookup.
func WithLogger(logger *slog.Logger) Option {
	return func(module *Module) {
		if logger != nil {
			module.logger = logger
		}
	}
}

// WithMaxEntries sets the in-memory cache capacity.
func WithMaxEntries(maxEntries int) Option {
	return func(module *Module) {
		if maxEntries > 0 {
			module.maxEntries = maxEntries
		}
	}
}

// WithTTL sets how long an entry can be returned without refresh.
func WithTTL(ttl time.Duration) Option {
	return func(module *Module) {
		if ttl > 0 {
			module.ttl = ttl
		}
	}
}

// Module stores message entity projections and per-message event history.
type Module struct {
	logger     *slog.Logger
	dispatcher otogi.OutboundDispatcher
	maxEntries int
	ttl        time.Duration
	clock      func() time.Time

	mu       sync.Mutex
	records  map[cacheKey]*cacheRecord
	entities map[cacheKey]otogi.CachedMessage
	events   map[cacheKey][]otogi.Event
	lru      *list.List
	index    map[cacheKey]*list.Element
}

type cacheKey struct {
	tenantID       string
	platform       otogi.Platform
	conversationID string
	messageID      string
}

type cacheRecord struct {
	expiresAt time.Time
}

type introspectionCommandKind string

const (
	introspectionCommandKindRaw     introspectionCommandKind = "raw"
	introspectionCommandKindHistory introspectionCommandKind = "history"
)

type introspectionCommand struct {
	kind      introspectionCommandKind
	messageID string
}

// New creates an event cache module with bounded in-memory storage.
func New(options ...Option) *Module {
	module := &Module{
		logger:     slog.Default(),
		maxEntries: defaultMaxEntries,
		ttl:        defaultTTL,
		clock:      time.Now,
		records:    make(map[cacheKey]*cacheRecord),
		entities:   make(map[cacheKey]otogi.CachedMessage),
		events:     make(map[cacheKey][]otogi.Event),
		lru:        list.New(),
		index:      make(map[cacheKey]*list.Element),
	}
	for _, option := range options {
		option(module)
	}

	return module
}

// Name returns the stable module identifier.
func (m *Module) Name() string {
	return "event-cache"
}

// Spec declares which events mutate the cache.
func (m *Module) Spec() otogi.ModuleSpec {
	return otogi.ModuleSpec{
		Handlers: []otogi.ModuleHandler{
			{
				Capability: otogi.Capability{
					Name:        "event-cache-writer",
					Description: "persists per-message event history, projects message entities, and handles ~raw/~history introspection commands",
					Interest: otogi.InterestSet{
						Kinds: []otogi.EventKind{
							otogi.EventKindMessageCreated,
							otogi.EventKindMessageEdited,
							otogi.EventKindMessageRetracted,
							otogi.EventKindReactionAdded,
							otogi.EventKindReactionRemoved,
						},
					},
					RequiredServices: []string{otogi.ServiceOutboundDispatcher},
				},
				Subscription: otogi.NewDefaultSubscriptionSpec("event-cache-writer"),
				Handler:      m.handleEvent,
			},
		},
	}
}

// OnRegister registers this module as the shared EventCache service.
func (m *Module) OnRegister(_ context.Context, runtime otogi.ModuleRuntime) error {
	logger, err := otogi.ResolveAs[*slog.Logger](runtime.Services(), ServiceLogger)
	switch {
	case err == nil:
		m.logger = logger
	case errors.Is(err, otogi.ErrServiceNotFound):
	default:
		return fmt.Errorf("event cache resolve logger: %w", err)
	}

	dispatcher, err := otogi.ResolveAs[otogi.OutboundDispatcher](
		runtime.Services(),
		otogi.ServiceOutboundDispatcher,
	)
	if err != nil {
		return fmt.Errorf("event cache resolve outbound dispatcher: %w", err)
	}
	m.dispatcher = dispatcher

	if err := runtime.Services().Register(otogi.ServiceEventCache, m); err != nil {
		return fmt.Errorf("event cache register service %s: %w", otogi.ServiceEventCache, err)
	}

	return nil
}

// OnStart starts the module lifecycle.
func (m *Module) OnStart(ctx context.Context) error {
	m.logger.InfoContext(ctx,
		"event cache module started",
		"module", m.Name(),
		"max_entries", m.maxEntries,
		"ttl", m.ttl,
	)

	return nil
}

// OnShutdown clears cached state.
func (m *Module) OnShutdown(ctx context.Context) error {
	m.mu.Lock()
	recordCount := len(m.records)
	m.records = make(map[cacheKey]*cacheRecord)
	m.entities = make(map[cacheKey]otogi.CachedMessage)
	m.events = make(map[cacheKey][]otogi.Event)
	m.index = make(map[cacheKey]*list.Element)
	m.lru.Init()
	m.mu.Unlock()

	m.logger.InfoContext(ctx,
		"event cache module shutdown",
		"module", m.Name(),
		"entries", recordCount,
	)

	return nil
}

// GetMessage returns one cached message entity projection.
func (m *Module) GetMessage(ctx context.Context, lookup otogi.MessageLookup) (otogi.CachedMessage, bool, error) {
	if err := ctx.Err(); err != nil {
		return otogi.CachedMessage{}, false, fmt.Errorf("event cache get message: %w", err)
	}
	if err := lookup.Validate(); err != nil {
		return otogi.CachedMessage{}, false, fmt.Errorf("event cache get message: %w", err)
	}

	now := m.now()
	key := cacheKeyFromLookup(lookup)

	m.mu.Lock()
	if !m.ensureNotExpiredLocked(key, now) {
		m.mu.Unlock()
		return otogi.CachedMessage{}, false, nil
	}
	m.touchLocked(key)
	cached, exists := m.entities[key]
	if !exists {
		m.mu.Unlock()
		return otogi.CachedMessage{}, false, nil
	}
	cloned := cloneCachedMessage(cached)
	m.mu.Unlock()

	return cloned, true, nil
}

// GetRepliedMessage resolves and returns the message referenced by ReplyToID.
func (m *Module) GetRepliedMessage(ctx context.Context, event *otogi.Event) (otogi.CachedMessage, bool, error) {
	if err := ctx.Err(); err != nil {
		return otogi.CachedMessage{}, false, fmt.Errorf("event cache get replied message: %w", err)
	}
	if event == nil {
		return otogi.CachedMessage{}, false, fmt.Errorf("event cache get replied message: nil event")
	}
	if event.Message == nil || event.Message.ReplyToID == "" {
		return otogi.CachedMessage{}, false, nil
	}

	lookup, err := otogi.ReplyLookupFromEvent(event)
	if err != nil {
		return otogi.CachedMessage{}, false, fmt.Errorf("event cache get replied message: %w", err)
	}

	return m.GetMessage(ctx, lookup)
}

// GetEvents returns event history captured for one message key.
func (m *Module) GetEvents(ctx context.Context, lookup otogi.MessageLookup) ([]otogi.Event, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, fmt.Errorf("event cache get events: %w", err)
	}
	if err := lookup.Validate(); err != nil {
		return nil, false, fmt.Errorf("event cache get events: %w", err)
	}

	now := m.now()
	key := cacheKeyFromLookup(lookup)

	m.mu.Lock()
	if !m.ensureNotExpiredLocked(key, now) {
		m.mu.Unlock()
		return nil, false, nil
	}
	m.touchLocked(key)
	history, exists := m.events[key]
	if !exists {
		m.mu.Unlock()
		return nil, false, nil
	}
	cloned := cloneEventStream(history)
	m.mu.Unlock()

	return cloned, true, nil
}

// GetRepliedEvents resolves and returns event history for ReplyToID.
func (m *Module) GetRepliedEvents(ctx context.Context, event *otogi.Event) ([]otogi.Event, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, fmt.Errorf("event cache get replied events: %w", err)
	}
	if event == nil {
		return nil, false, fmt.Errorf("event cache get replied events: nil event")
	}
	if event.Message == nil || event.Message.ReplyToID == "" {
		return nil, false, nil
	}

	lookup, err := otogi.ReplyLookupFromEvent(event)
	if err != nil {
		return nil, false, fmt.Errorf("event cache get replied events: %w", err)
	}

	return m.GetEvents(ctx, lookup)
}

func (m *Module) handleEvent(ctx context.Context, event *otogi.Event) error {
	if event == nil {
		return fmt.Errorf("event cache handle event: nil event")
	}

	switch event.Kind {
	case otogi.EventKindMessageCreated:
		if err := m.appendEvent(event); err != nil {
			return fmt.Errorf("event cache handle %s append event: %w", event.Kind, err)
		}
		if err := m.rememberCreated(event); err != nil {
			return fmt.Errorf("event cache handle %s project entity: %w", event.Kind, err)
		}
		if err := m.handleIntrospectionCommand(ctx, event); err != nil {
			return fmt.Errorf("event cache handle introspection command: %w", err)
		}
	case otogi.EventKindMessageEdited:
		if err := m.appendEvent(event); err != nil {
			return fmt.Errorf("event cache handle %s append event: %w", event.Kind, err)
		}
		if err := m.rememberEdit(event); err != nil {
			return fmt.Errorf("event cache handle %s project entity: %w", event.Kind, err)
		}
	case otogi.EventKindMessageRetracted:
		if err := m.appendEvent(event); err != nil {
			return fmt.Errorf("event cache handle %s append event: %w", event.Kind, err)
		}
		if err := m.forgetRetracted(event); err != nil {
			return fmt.Errorf("event cache handle %s project entity: %w", event.Kind, err)
		}
	case otogi.EventKindReactionAdded, otogi.EventKindReactionRemoved:
		if err := m.appendEvent(event); err != nil {
			return fmt.Errorf("event cache handle %s append event: %w", event.Kind, err)
		}
		if err := m.rememberReaction(event); err != nil {
			return fmt.Errorf("event cache handle %s project entity: %w", event.Kind, err)
		}
	default:
	}

	return nil
}

func (m *Module) handleIntrospectionCommand(ctx context.Context, event *otogi.Event) error {
	if event == nil || event.Message == nil {
		return nil
	}

	command, matched, parseErr := parseIntrospectionCommand(event.Message.Text)
	if !matched {
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
		return m.replyCommandResult(ctx, target, event.Message.ID, parseErr.Error())
	}

	lookup, hasTarget, err := commandLookup(event, command.messageID)
	if err != nil {
		return fmt.Errorf("%s command resolve target lookup: %w", command.kind, err)
	}
	if !hasTarget {
		return nil
	}

	var body string
	switch command.kind {
	case introspectionCommandKindRaw:
		entity, found, getErr := m.GetMessage(ctx, lookup)
		if getErr != nil {
			return fmt.Errorf("raw command resolve message entity: %w", getErr)
		}
		if !found {
			return m.replyCommandResult(
				ctx,
				target,
				event.Message.ID,
				notFoundMessage(command.kind, command.messageID != ""),
			)
		}

		formattedBody, formatErr := formatRawEntity(entity)
		if formatErr != nil {
			return fmt.Errorf("raw command format entity: %w", formatErr)
		}
		body = formattedBody
	case introspectionCommandKindHistory:
		history, found, getErr := m.GetEvents(ctx, lookup)
		if getErr != nil {
			return fmt.Errorf("history command resolve event history: %w", getErr)
		}
		if !found {
			return m.replyCommandResult(
				ctx,
				target,
				event.Message.ID,
				notFoundMessage(command.kind, command.messageID != ""),
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

	return m.replyCommandResult(ctx, target, event.Message.ID, trimForCommandReply(body))
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

func commandReplyEntities(body string) []otogi.TextEntity {
	if body == "" {
		return nil
	}

	return []otogi.TextEntity{
		{
			Type:     otogi.TextEntityTypePre,
			Offset:   0,
			Length:   utf8.RuneCountInString(body),
			Language: "json",
		},
	}
}

func parseIntrospectionCommand(text string) (introspectionCommand, bool, error) {
	command := introspectionCommand{}

	fields := strings.Fields(strings.TrimSpace(text))
	if len(fields) == 0 {
		return command, false, nil
	}

	name, matched := matchCommandName(fields[0])
	if !matched {
		return command, false, nil
	}
	command.kind = name

	if len(fields[1:]) > maxCommandArgumentSize {
		return command, true, fmt.Errorf("%s: expected at most one integer message id argument", command.kind)
	}

	if len(fields) == 2 {
		messageID, err := parseMessageIDArgument(fields[1])
		if err != nil {
			return command, true, fmt.Errorf("%s: %w", command.kind, err)
		}
		command.messageID = messageID
	}

	return command, true, nil
}

func matchCommandName(token string) (introspectionCommandKind, bool) {
	command := strings.ToLower(strings.TrimSpace(token))
	if command == "" {
		return "", false
	}

	if mentionSeparator := strings.Index(command, "@"); mentionSeparator >= 0 {
		command = command[:mentionSeparator]
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

func parseMessageIDArgument(argument string) (string, error) {
	messageID, err := strconv.ParseInt(argument, 10, 64)
	if err != nil || messageID <= 0 {
		return "", fmt.Errorf("invalid message id %q, expected a positive integer", argument)
	}

	return strconv.FormatInt(messageID, 10), nil
}

func commandLookup(event *otogi.Event, explicitMessageID string) (otogi.MessageLookup, bool, error) {
	if event == nil {
		return otogi.MessageLookup{}, false, fmt.Errorf("nil event")
	}

	if explicitMessageID != "" {
		lookup := otogi.MessageLookup{
			TenantID:       event.TenantID,
			Platform:       event.Platform,
			ConversationID: event.Conversation.ID,
			MessageID:      explicitMessageID,
		}
		if err := lookup.Validate(); err != nil {
			return otogi.MessageLookup{}, false, fmt.Errorf("explicit message id lookup: %w", err)
		}

		return lookup, true, nil
	}

	if event.Message == nil || event.Message.ReplyToID == "" {
		return otogi.MessageLookup{}, false, nil
	}

	lookup, err := otogi.ReplyLookupFromEvent(event)
	if err != nil {
		return otogi.MessageLookup{}, false, fmt.Errorf("reply lookup: %w", err)
	}

	return lookup, true, nil
}

func notFoundMessage(kind introspectionCommandKind, explicitLookup bool) string {
	if explicitLookup {
		return fmt.Sprintf("%s: message not found in event cache", kind)
	}

	return fmt.Sprintf("%s: replied message not found in event cache", kind)
}

func trimForCommandReply(body string) string {
	if len(body) <= maxCommandReplyLength {
		return body
	}

	return body[:maxCommandReplyLength] + "\n...(truncated)"
}

func formatRawEntity(cached otogi.CachedMessage) (string, error) {
	body, err := json.MarshalIndent(cached, "", "  ")
	if err != nil {
		return "", fmt.Errorf("format raw entity json: %w", err)
	}

	return string(body), nil
}

func formatHistoryEvents(events []otogi.Event) (string, error) {
	body, err := json.MarshalIndent(events, "", "  ")
	if err != nil {
		return "", fmt.Errorf("format history events json: %w", err)
	}

	return string(body), nil
}

func (m *Module) rememberCreated(event *otogi.Event) error {
	lookup, err := otogi.MessageLookupFromEvent(event)
	if err != nil {
		return fmt.Errorf("remember created: %w", err)
	}

	now := m.now()
	occurredAt := normalizeEventTime(event.OccurredAt, now)
	updatedAt := mutationChangedAtOrFallback(event.Mutation, occurredAt)
	cached := otogi.CachedMessage{
		TenantID:     event.TenantID,
		Platform:     event.Platform,
		Conversation: event.Conversation,
		Actor:        event.Actor,
		Message:      cloneMessage(*event.Message),
		CreatedAt:    occurredAt,
		UpdatedAt:    updatedAt,
	}

	key := cacheKeyFromLookup(lookup)

	m.mu.Lock()
	m.ensureNotExpiredLocked(key, now)
	if existing, exists := m.entities[key]; exists {
		if !existing.CreatedAt.IsZero() {
			cached.CreatedAt = existing.CreatedAt
		}
		if len(cached.Message.Reactions) == 0 && len(existing.Message.Reactions) > 0 {
			cached.Message.Reactions = cloneMessageReactions(existing.Message.Reactions)
		}
		if !existing.UpdatedAt.IsZero() && cached.UpdatedAt.Before(existing.UpdatedAt) {
			cached.UpdatedAt = existing.UpdatedAt
		}
	}
	if len(cached.Message.Reactions) == 0 {
		if history, exists := m.events[key]; exists {
			applyReactionHistoryToMessage(&cached.Message, history, cached.Message.ID)
		}
	}
	if event.Reaction != nil && event.Reaction.MessageID == cached.Message.ID {
		applyReactionToMessage(&cached.Message, *event.Reaction)
	}
	if cached.UpdatedAt.IsZero() {
		cached.UpdatedAt = cached.CreatedAt
	}
	m.upsertEntityLocked(key, cached, now)
	m.mu.Unlock()

	return nil
}

func (m *Module) rememberEdit(event *otogi.Event) error {
	if event == nil {
		return fmt.Errorf("remember edit: nil event")
	}
	lookup, err := lookupFromMutation(event)
	if err != nil {
		return fmt.Errorf("remember edit: %w", err)
	}

	now := m.now()
	occurredAt := normalizeEventTime(event.OccurredAt, now)
	updatedAt := mutationChangedAtOrFallback(event.Mutation, occurredAt)
	key := cacheKeyFromLookup(lookup)

	m.mu.Lock()
	m.ensureNotExpiredLocked(key, now)
	cached := otogi.CachedMessage{
		TenantID:     lookup.TenantID,
		Platform:     lookup.Platform,
		Conversation: event.Conversation,
		Actor:        event.Actor,
		Message: otogi.Message{
			ID: lookup.MessageID,
		},
		CreatedAt: occurredAt,
		UpdatedAt: occurredAt,
	}
	if existing, exists := m.entities[key]; exists {
		cached = cloneCachedMessage(existing)
	}
	if len(cached.Message.Reactions) == 0 {
		if history, exists := m.events[key]; exists {
			applyReactionHistoryToMessage(&cached.Message, history, lookup.MessageID)
		}
	}
	if cached.Conversation.ID == "" {
		cached.Conversation = event.Conversation
	}
	if cached.Platform == "" {
		cached.Platform = event.Platform
	}
	if cached.Message.ID == "" {
		cached.Message.ID = lookup.MessageID
	}
	if event.Mutation.After != nil {
		cached.Message.Text = event.Mutation.After.Text
		cached.Message.Media = cloneMediaAttachments(event.Mutation.After.Media)
	}
	cached.UpdatedAt = updatedAt
	if cached.CreatedAt.IsZero() {
		cached.CreatedAt = occurredAt
	}
	m.upsertEntityLocked(key, cached, now)
	m.mu.Unlock()

	return nil
}

func (m *Module) rememberReaction(event *otogi.Event) error {
	if event == nil {
		return fmt.Errorf("remember reaction: nil event")
	}
	lookup, err := lookupFromReaction(event)
	if err != nil {
		return fmt.Errorf("remember reaction: %w", err)
	}

	now := m.now()
	key := cacheKeyFromLookup(lookup)

	m.mu.Lock()
	m.ensureNotExpiredLocked(key, now)
	existing, exists := m.entities[key]
	if !exists {
		m.mu.Unlock()
		return nil
	}

	cached := cloneCachedMessage(existing)
	applyReactionToMessage(&cached.Message, *event.Reaction)
	m.upsertEntityLocked(key, cached, now)
	m.mu.Unlock()

	return nil
}

func (m *Module) forgetRetracted(event *otogi.Event) error {
	if event == nil {
		return fmt.Errorf("forget retracted: nil event")
	}
	lookup, err := lookupFromMutation(event)
	if err != nil {
		return fmt.Errorf("forget retracted: %w", err)
	}

	now := m.now()
	key := cacheKeyFromLookup(lookup)

	m.mu.Lock()
	m.ensureNotExpiredLocked(key, now)
	delete(m.entities, key)
	m.upsertKeyLocked(key, now)
	m.trimToCapacityLocked()
	m.mu.Unlock()

	return nil
}

func (m *Module) appendEvent(event *otogi.Event) error {
	if event == nil {
		return fmt.Errorf("append event: nil event")
	}
	lookup, err := lookupFromEvent(event)
	if err != nil {
		return fmt.Errorf("append event: %w", err)
	}

	now := m.now()
	key := cacheKeyFromLookup(lookup)

	m.mu.Lock()
	m.upsertEventLocked(key, event, now)
	m.mu.Unlock()

	return nil
}

func lookupFromEvent(event *otogi.Event) (otogi.MessageLookup, error) {
	if event == nil {
		return otogi.MessageLookup{}, fmt.Errorf("lookup from event: nil event")
	}

	switch event.Kind {
	case otogi.EventKindMessageCreated:
		lookup, err := otogi.MessageLookupFromEvent(event)
		if err != nil {
			return otogi.MessageLookup{}, fmt.Errorf("lookup from event %s: %w", event.Kind, err)
		}
		return lookup, nil
	case otogi.EventKindMessageEdited, otogi.EventKindMessageRetracted:
		lookup, err := lookupFromMutation(event)
		if err != nil {
			return otogi.MessageLookup{}, fmt.Errorf("lookup from event %s: %w", event.Kind, err)
		}
		return lookup, nil
	case otogi.EventKindReactionAdded, otogi.EventKindReactionRemoved:
		lookup, err := lookupFromReaction(event)
		if err != nil {
			return otogi.MessageLookup{}, fmt.Errorf("lookup from event %s: %w", event.Kind, err)
		}
		return lookup, nil
	default:
		return otogi.MessageLookup{}, fmt.Errorf("lookup from event: unsupported kind %s", event.Kind)
	}
}

func lookupFromMutation(event *otogi.Event) (otogi.MessageLookup, error) {
	if event == nil {
		return otogi.MessageLookup{}, fmt.Errorf("lookup from mutation: nil event")
	}
	if event.Mutation == nil {
		return otogi.MessageLookup{}, fmt.Errorf("lookup from mutation: missing mutation payload")
	}
	if event.Mutation.TargetMessageID == "" {
		return otogi.MessageLookup{}, fmt.Errorf("lookup from mutation: missing target message id")
	}

	lookup := otogi.MessageLookup{
		TenantID:       event.TenantID,
		Platform:       event.Platform,
		ConversationID: event.Conversation.ID,
		MessageID:      event.Mutation.TargetMessageID,
	}
	if err := lookup.Validate(); err != nil {
		return otogi.MessageLookup{}, fmt.Errorf("lookup from mutation: %w", err)
	}

	return lookup, nil
}

func lookupFromReaction(event *otogi.Event) (otogi.MessageLookup, error) {
	if event == nil {
		return otogi.MessageLookup{}, fmt.Errorf("lookup from reaction: nil event")
	}
	if event.Reaction == nil {
		return otogi.MessageLookup{}, fmt.Errorf("lookup from reaction: missing reaction payload")
	}
	if event.Reaction.MessageID == "" {
		return otogi.MessageLookup{}, fmt.Errorf("lookup from reaction: missing reaction message id")
	}

	lookup := otogi.MessageLookup{
		TenantID:       event.TenantID,
		Platform:       event.Platform,
		ConversationID: event.Conversation.ID,
		MessageID:      event.Reaction.MessageID,
	}
	if err := lookup.Validate(); err != nil {
		return otogi.MessageLookup{}, fmt.Errorf("lookup from reaction: %w", err)
	}

	return lookup, nil
}

func (m *Module) upsertEntityLocked(key cacheKey, cached otogi.CachedMessage, now time.Time) {
	m.upsertKeyLocked(key, now)
	m.entities[key] = cloneCachedMessage(cached)
	m.trimToCapacityLocked()
}

func (m *Module) upsertEventLocked(key cacheKey, event *otogi.Event, now time.Time) {
	if event == nil {
		return
	}

	cloned := cloneEvent(*event)
	cloned = m.enrichHistoryEventLocked(key, cloned)
	m.upsertKeyLocked(key, now)
	m.events[key] = append(m.events[key], cloned)
	m.trimToCapacityLocked()
}

func (m *Module) enrichHistoryEventLocked(key cacheKey, event otogi.Event) otogi.Event {
	if event.Kind != otogi.EventKindMessageEdited {
		return event
	}
	if event.Mutation == nil || event.Mutation.Before != nil {
		return event
	}

	existing, exists := m.entities[key]
	if !exists {
		return event
	}
	event.Mutation.Before = &otogi.MessageSnapshot{
		Text:  existing.Message.Text,
		Media: cloneMediaAttachments(existing.Message.Media),
	}

	return event
}

func (m *Module) upsertKeyLocked(key cacheKey, now time.Time) {
	if m.ensureNotExpiredLocked(key, now) {
		record := m.records[key]
		record.expiresAt = m.expiryFrom(now)
		m.touchLocked(key)
		return
	}

	element := m.lru.PushFront(key)
	m.index[key] = element
	m.records[key] = &cacheRecord{expiresAt: m.expiryFrom(now)}
}

func (m *Module) ensureNotExpiredLocked(key cacheKey, now time.Time) bool {
	record, exists := m.records[key]
	if !exists {
		return false
	}
	if m.isExpired(record, now) {
		m.deleteLocked(key)
		return false
	}

	return true
}

func (m *Module) trimToCapacityLocked() {
	for len(m.records) > m.maxEntries {
		back := m.lru.Back()
		if back == nil {
			break
		}
		oldestKey, ok := back.Value.(cacheKey)
		if !ok {
			m.lru.Remove(back)
			continue
		}
		m.deleteLocked(oldestKey)
	}
}

func (m *Module) touchLocked(key cacheKey) {
	element, exists := m.index[key]
	if !exists {
		return
	}
	m.lru.MoveToFront(element)
}

func (m *Module) deleteLocked(key cacheKey) {
	if element, exists := m.index[key]; exists {
		m.lru.Remove(element)
		delete(m.index, key)
	}
	delete(m.records, key)
	delete(m.entities, key)
	delete(m.events, key)
}

func (m *Module) isExpired(record *cacheRecord, now time.Time) bool {
	if record == nil {
		return true
	}
	if record.expiresAt.IsZero() {
		return false
	}

	return !now.Before(record.expiresAt)
}

func (m *Module) expiryFrom(now time.Time) time.Time {
	if m.ttl <= 0 {
		return time.Time{}
	}

	return now.Add(m.ttl)
}

func (m *Module) now() time.Time {
	return m.clock().UTC()
}

func cacheKeyFromLookup(lookup otogi.MessageLookup) cacheKey {
	return cacheKey{
		tenantID:       lookup.TenantID,
		platform:       lookup.Platform,
		conversationID: lookup.ConversationID,
		messageID:      lookup.MessageID,
	}
}

func normalizeEventTime(occurredAt time.Time, fallback time.Time) time.Time {
	if occurredAt.IsZero() {
		return fallback
	}

	return occurredAt.UTC()
}

func mutationChangedAtOrFallback(mutation *otogi.Mutation, fallback time.Time) time.Time {
	if mutation == nil || mutation.ChangedAt == nil || mutation.ChangedAt.IsZero() {
		return fallback
	}

	return mutation.ChangedAt.UTC()
}

func cloneCachedMessage(cached otogi.CachedMessage) otogi.CachedMessage {
	cloned := cached
	cloned.Message = cloneMessage(cached.Message)

	return cloned
}

func cloneEvent(event otogi.Event) otogi.Event {
	cloned := event
	if event.Message != nil {
		message := cloneMessage(*event.Message)
		cloned.Message = &message
	}
	if event.Mutation != nil {
		cloned.Mutation = cloneMutation(event.Mutation)
	}
	if event.Reaction != nil {
		reaction := *event.Reaction
		cloned.Reaction = &reaction
	}
	if event.StateChange != nil {
		cloned.StateChange = cloneStateChange(event.StateChange)
	}
	if len(event.Metadata) > 0 {
		cloned.Metadata = make(map[string]string, len(event.Metadata))
		for key, value := range event.Metadata {
			cloned.Metadata[key] = value
		}
	}

	return cloned
}

func cloneEventStream(events []otogi.Event) []otogi.Event {
	if len(events) == 0 {
		return nil
	}

	cloned := make([]otogi.Event, len(events))
	for idx, event := range events {
		cloned[idx] = cloneEvent(event)
	}

	return cloned
}

func cloneMutation(mutation *otogi.Mutation) *otogi.Mutation {
	if mutation == nil {
		return nil
	}
	cloned := *mutation
	if mutation.ChangedAt != nil {
		changedAt := *mutation.ChangedAt
		cloned.ChangedAt = &changedAt
	}
	if mutation.Before != nil {
		before := cloneMessageSnapshot(*mutation.Before)
		cloned.Before = &before
	}
	if mutation.After != nil {
		after := cloneMessageSnapshot(*mutation.After)
		cloned.After = &after
	}

	return &cloned
}

func cloneMessageSnapshot(snapshot otogi.MessageSnapshot) otogi.MessageSnapshot {
	cloned := snapshot
	if len(snapshot.Media) > 0 {
		cloned.Media = cloneMediaAttachments(snapshot.Media)
	} else {
		cloned.Media = nil
	}

	return cloned
}

func cloneStateChange(state *otogi.StateChange) *otogi.StateChange {
	if state == nil {
		return nil
	}
	cloned := *state
	if state.Member != nil {
		member := *state.Member
		if state.Member.Inviter != nil {
			inviter := *state.Member.Inviter
			member.Inviter = &inviter
		}
		cloned.Member = &member
	}
	if state.Role != nil {
		role := *state.Role
		cloned.Role = &role
	}
	if state.Migration != nil {
		migration := *state.Migration
		cloned.Migration = &migration
	}

	return &cloned
}

func cloneMessage(message otogi.Message) otogi.Message {
	cloned := message
	if len(message.Entities) > 0 {
		cloned.Entities = append([]otogi.TextEntity(nil), message.Entities...)
	} else {
		cloned.Entities = nil
	}
	if len(message.Media) > 0 {
		cloned.Media = cloneMediaAttachments(message.Media)
	} else {
		cloned.Media = nil
	}
	if len(message.Reactions) > 0 {
		cloned.Reactions = cloneMessageReactions(message.Reactions)
	} else {
		cloned.Reactions = nil
	}

	return cloned
}

func cloneMessageReactions(reactions []otogi.MessageReaction) []otogi.MessageReaction {
	if len(reactions) == 0 {
		return nil
	}

	cloned := make([]otogi.MessageReaction, len(reactions))
	copy(cloned, reactions)

	return cloned
}

func applyReactionToMessage(message *otogi.Message, reaction otogi.Reaction) {
	if message == nil {
		return
	}
	if reaction.Emoji == "" {
		return
	}

	index := -1
	for idx := range message.Reactions {
		if message.Reactions[idx].Emoji == reaction.Emoji {
			index = idx
			break
		}
	}

	switch reaction.Action {
	case otogi.ReactionActionAdd:
		if index >= 0 {
			message.Reactions[index].Count++
			return
		}
		message.Reactions = append(message.Reactions, otogi.MessageReaction{
			Emoji: reaction.Emoji,
			Count: 1,
		})
	case otogi.ReactionActionRemove:
		if index < 0 {
			return
		}
		if message.Reactions[index].Count <= 1 {
			message.Reactions = append(message.Reactions[:index], message.Reactions[index+1:]...)
			return
		}
		message.Reactions[index].Count--
	default:
	}
}

func applyReactionHistoryToMessage(message *otogi.Message, history []otogi.Event, messageID string) {
	if message == nil || len(history) == 0 {
		return
	}

	for _, event := range history {
		if event.Reaction == nil {
			continue
		}
		if event.Kind != otogi.EventKindReactionAdded && event.Kind != otogi.EventKindReactionRemoved {
			continue
		}
		if messageID != "" && event.Reaction.MessageID != "" && event.Reaction.MessageID != messageID {
			continue
		}
		applyReactionToMessage(message, *event.Reaction)
	}
}

func cloneMediaAttachments(media []otogi.MediaAttachment) []otogi.MediaAttachment {
	if len(media) == 0 {
		return nil
	}

	cloned := make([]otogi.MediaAttachment, len(media))
	for idx, attachment := range media {
		attachmentClone := attachment
		if attachment.Preview != nil {
			previewClone := *attachment.Preview
			if len(attachment.Preview.Bytes) > 0 {
				previewClone.Bytes = append([]byte(nil), attachment.Preview.Bytes...)
			}
			attachmentClone.Preview = &previewClone
		}
		cloned[idx] = attachmentClone
	}

	return cloned
}

func withClock(clock func() time.Time) Option {
	return func(module *Module) {
		if clock != nil {
			module.clock = clock
		}
	}
}

var (
	_ otogi.Module          = (*Module)(nil)
	_ otogi.ModuleRegistrar = (*Module)(nil)
	_ otogi.EventCache      = (*Module)(nil)
)

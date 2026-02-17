package articlecache

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

// Option mutates article cache module configuration.
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

// Module stores article projections and per-article event history.
type Module struct {
	logger     *slog.Logger
	dispatcher otogi.OutboundDispatcher
	maxEntries int
	ttl        time.Duration
	clock      func() time.Time

	mu       sync.Mutex
	records  map[cacheKey]*cacheRecord
	entities map[cacheKey]otogi.CachedArticle
	events   map[cacheKey][]otogi.Event
	lru      *list.List
	index    map[cacheKey]*list.Element
}

type cacheKey struct {
	tenantID       string
	platform       otogi.Platform
	conversationID string
	articleID      string
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
	articleID string
}

// New creates an article cache module with bounded in-memory storage.
func New(options ...Option) *Module {
	module := &Module{
		logger:     slog.Default(),
		maxEntries: defaultMaxEntries,
		ttl:        defaultTTL,
		clock:      time.Now,
		records:    make(map[cacheKey]*cacheRecord),
		entities:   make(map[cacheKey]otogi.CachedArticle),
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
	return "article-cache"
}

// Spec declares which events mutate the cache.
func (m *Module) Spec() otogi.ModuleSpec {
	return otogi.ModuleSpec{
		Handlers: []otogi.ModuleHandler{
			{
				Capability: otogi.Capability{
					Name:        "article-cache-writer",
					Description: "persists per-article event history, projects article state, and handles ~raw/~history introspection commands",
					Interest: otogi.InterestSet{
						Kinds: []otogi.EventKind{
							otogi.EventKindArticleCreated,
							otogi.EventKindArticleEdited,
							otogi.EventKindArticleRetracted,
							otogi.EventKindArticleReactionAdded,
							otogi.EventKindArticleReactionRemoved,
						},
					},
					RequiredServices: []string{otogi.ServiceOutboundDispatcher},
				},
				Subscription: otogi.NewDefaultSubscriptionSpec("article-cache-writer"),
				Handler:      m.handleEvent,
			},
		},
	}
}

// OnRegister registers this module as the shared ArticleStore service.
func (m *Module) OnRegister(_ context.Context, runtime otogi.ModuleRuntime) error {
	logger, err := otogi.ResolveAs[*slog.Logger](runtime.Services(), ServiceLogger)
	switch {
	case err == nil:
		m.logger = logger
	case errors.Is(err, otogi.ErrServiceNotFound):
	default:
		return fmt.Errorf("article cache resolve logger: %w", err)
	}

	dispatcher, err := otogi.ResolveAs[otogi.OutboundDispatcher](
		runtime.Services(),
		otogi.ServiceOutboundDispatcher,
	)
	if err != nil {
		return fmt.Errorf("article cache resolve outbound dispatcher: %w", err)
	}
	m.dispatcher = dispatcher

	if err := runtime.Services().Register(otogi.ServiceArticleStore, m); err != nil {
		return fmt.Errorf("article cache register service %s: %w", otogi.ServiceArticleStore, err)
	}

	return nil
}

// OnStart starts the module lifecycle.
func (m *Module) OnStart(ctx context.Context) error {
	m.logger.InfoContext(ctx,
		"article cache module started",
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
	m.entities = make(map[cacheKey]otogi.CachedArticle)
	m.events = make(map[cacheKey][]otogi.Event)
	m.index = make(map[cacheKey]*list.Element)
	m.lru.Init()
	m.mu.Unlock()

	m.logger.InfoContext(ctx,
		"article cache module shutdown",
		"module", m.Name(),
		"entries", recordCount,
	)

	return nil
}

// GetArticle returns one cached article projection.
func (m *Module) GetArticle(ctx context.Context, lookup otogi.ArticleLookup) (otogi.CachedArticle, bool, error) {
	if err := ctx.Err(); err != nil {
		return otogi.CachedArticle{}, false, fmt.Errorf("article cache get article: %w", err)
	}
	if err := lookup.Validate(); err != nil {
		return otogi.CachedArticle{}, false, fmt.Errorf("article cache get article: %w", err)
	}

	now := m.now()
	key := cacheKeyFromLookup(lookup)

	m.mu.Lock()
	if !m.ensureNotExpiredLocked(key, now) {
		m.mu.Unlock()
		return otogi.CachedArticle{}, false, nil
	}
	m.touchLocked(key)
	cached, exists := m.entities[key]
	if !exists {
		m.mu.Unlock()
		return otogi.CachedArticle{}, false, nil
	}
	cloned := cloneCachedArticle(cached)
	m.mu.Unlock()

	return cloned, true, nil
}

// GetRepliedArticle resolves and returns the article referenced by ReplyToArticleID.
func (m *Module) GetRepliedArticle(ctx context.Context, event *otogi.Event) (otogi.CachedArticle, bool, error) {
	if err := ctx.Err(); err != nil {
		return otogi.CachedArticle{}, false, fmt.Errorf("article cache get replied article: %w", err)
	}
	if event == nil {
		return otogi.CachedArticle{}, false, fmt.Errorf("article cache get replied article: nil event")
	}
	if event.Article == nil || event.Article.ReplyToArticleID == "" {
		return otogi.CachedArticle{}, false, nil
	}

	lookup, err := otogi.ReplyArticleLookupFromEvent(event)
	if err != nil {
		return otogi.CachedArticle{}, false, fmt.Errorf("article cache get replied article: %w", err)
	}

	return m.GetArticle(ctx, lookup)
}

// GetEvents returns event history captured for one article key.
func (m *Module) GetEvents(ctx context.Context, lookup otogi.ArticleLookup) ([]otogi.Event, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, fmt.Errorf("article cache get events: %w", err)
	}
	if err := lookup.Validate(); err != nil {
		return nil, false, fmt.Errorf("article cache get events: %w", err)
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

// GetRepliedEvents resolves and returns event history for ReplyToArticleID.
func (m *Module) GetRepliedEvents(ctx context.Context, event *otogi.Event) ([]otogi.Event, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, fmt.Errorf("article cache get replied events: %w", err)
	}
	if event == nil {
		return nil, false, fmt.Errorf("article cache get replied events: nil event")
	}
	if event.Article == nil || event.Article.ReplyToArticleID == "" {
		return nil, false, nil
	}

	lookup, err := otogi.ReplyArticleLookupFromEvent(event)
	if err != nil {
		return nil, false, fmt.Errorf("article cache get replied events: %w", err)
	}

	return m.GetEvents(ctx, lookup)
}

func (m *Module) handleEvent(ctx context.Context, event *otogi.Event) error {
	if event == nil {
		return fmt.Errorf("article cache handle event: nil event")
	}

	switch event.Kind {
	case otogi.EventKindArticleCreated:
		if err := m.appendEvent(event); err != nil {
			return fmt.Errorf("article cache handle %s append event: %w", event.Kind, err)
		}
		if err := m.rememberCreated(event); err != nil {
			return fmt.Errorf("article cache handle %s project article: %w", event.Kind, err)
		}
		if err := m.handleIntrospectionCommand(ctx, event); err != nil {
			return fmt.Errorf("article cache handle introspection command: %w", err)
		}
	case otogi.EventKindArticleEdited:
		if err := m.appendEvent(event); err != nil {
			return fmt.Errorf("article cache handle %s append event: %w", event.Kind, err)
		}
		if err := m.rememberEdit(event); err != nil {
			return fmt.Errorf("article cache handle %s project article: %w", event.Kind, err)
		}
	case otogi.EventKindArticleRetracted:
		if err := m.appendEvent(event); err != nil {
			return fmt.Errorf("article cache handle %s append event: %w", event.Kind, err)
		}
		if err := m.forgetRetracted(event); err != nil {
			return fmt.Errorf("article cache handle %s project article: %w", event.Kind, err)
		}
	case otogi.EventKindArticleReactionAdded, otogi.EventKindArticleReactionRemoved:
		if err := m.appendEvent(event); err != nil {
			return fmt.Errorf("article cache handle %s append event: %w", event.Kind, err)
		}
		if err := m.rememberReaction(event); err != nil {
			return fmt.Errorf("article cache handle %s project article: %w", event.Kind, err)
		}
	default:
	}

	return nil
}

func (m *Module) handleIntrospectionCommand(ctx context.Context, event *otogi.Event) error {
	if event == nil || event.Article == nil {
		return nil
	}

	command, matched, parseErr := parseIntrospectionCommand(event.Article.Text)
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
		entity, found, getErr := m.GetArticle(ctx, lookup)
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
		history, found, getErr := m.GetEvents(ctx, lookup)
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
		return command, true, fmt.Errorf("%s: expected at most one integer article id argument", command.kind)
	}

	if len(fields) == 2 {
		articleID, err := parseArticleIDArgument(fields[1])
		if err != nil {
			return command, true, fmt.Errorf("%s: %w", command.kind, err)
		}
		command.articleID = articleID
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

func parseArticleIDArgument(argument string) (string, error) {
	articleID, err := strconv.ParseInt(argument, 10, 64)
	if err != nil || articleID <= 0 {
		return "", fmt.Errorf("invalid article id %q, expected a positive integer", argument)
	}

	return strconv.FormatInt(articleID, 10), nil
}

func commandLookup(event *otogi.Event, explicitArticleID string) (otogi.ArticleLookup, bool, error) {
	if event == nil {
		return otogi.ArticleLookup{}, false, fmt.Errorf("nil event")
	}

	if explicitArticleID != "" {
		lookup := otogi.ArticleLookup{
			TenantID:       event.TenantID,
			Platform:       event.Platform,
			ConversationID: event.Conversation.ID,
			ArticleID:      explicitArticleID,
		}
		if err := lookup.Validate(); err != nil {
			return otogi.ArticleLookup{}, false, fmt.Errorf("explicit article id lookup: %w", err)
		}

		return lookup, true, nil
	}

	if event.Article == nil || event.Article.ReplyToArticleID == "" {
		return otogi.ArticleLookup{}, false, nil
	}

	lookup, err := otogi.ReplyArticleLookupFromEvent(event)
	if err != nil {
		return otogi.ArticleLookup{}, false, fmt.Errorf("reply lookup: %w", err)
	}

	return lookup, true, nil
}

func notFoundMessage(kind introspectionCommandKind, explicitLookup bool) string {
	if explicitLookup {
		return fmt.Sprintf("%s: article not found in article cache", kind)
	}

	return fmt.Sprintf("%s: replied article not found in article cache", kind)
}

func trimForCommandReply(body string) string {
	if len(body) <= maxCommandReplyLength {
		return body
	}

	return body[:maxCommandReplyLength] + "\n...(truncated)"
}

func formatRawEntity(cached otogi.CachedArticle) (string, error) {
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
	lookup, err := otogi.ArticleLookupFromEvent(event)
	if err != nil {
		return fmt.Errorf("remember created: %w", err)
	}

	now := m.now()
	occurredAt := normalizeEventTime(event.OccurredAt, now)
	updatedAt := mutationChangedAtOrFallback(event.Mutation, occurredAt)
	cached := otogi.CachedArticle{
		TenantID:     event.TenantID,
		Platform:     event.Platform,
		Conversation: event.Conversation,
		Actor:        event.Actor,
		Article:      cloneArticle(*event.Article),
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
		if len(cached.Article.Reactions) == 0 && len(existing.Article.Reactions) > 0 {
			cached.Article.Reactions = cloneArticleReactions(existing.Article.Reactions)
		}
		if !existing.UpdatedAt.IsZero() && cached.UpdatedAt.Before(existing.UpdatedAt) {
			cached.UpdatedAt = existing.UpdatedAt
		}
	}
	if len(cached.Article.Reactions) == 0 {
		if history, exists := m.events[key]; exists {
			applyReactionHistoryToArticle(&cached.Article, history, cached.Article.ID)
		}
	}
	if event.Reaction != nil && event.Reaction.ArticleID == cached.Article.ID {
		applyReactionToArticle(&cached.Article, *event.Reaction)
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
	lookup, err := otogi.MutationArticleLookupFromEvent(event)
	if err != nil {
		return fmt.Errorf("remember edit: %w", err)
	}

	now := m.now()
	occurredAt := normalizeEventTime(event.OccurredAt, now)
	updatedAt := mutationChangedAtOrFallback(event.Mutation, occurredAt)
	key := cacheKeyFromLookup(lookup)

	m.mu.Lock()
	m.ensureNotExpiredLocked(key, now)
	cached := otogi.CachedArticle{
		TenantID:     lookup.TenantID,
		Platform:     lookup.Platform,
		Conversation: event.Conversation,
		Actor:        event.Actor,
		Article: otogi.Article{
			ID: lookup.ArticleID,
		},
		CreatedAt: occurredAt,
		UpdatedAt: occurredAt,
	}
	if existing, exists := m.entities[key]; exists {
		cached = cloneCachedArticle(existing)
	}
	if len(cached.Article.Reactions) == 0 {
		if history, exists := m.events[key]; exists {
			applyReactionHistoryToArticle(&cached.Article, history, lookup.ArticleID)
		}
	}
	if cached.Conversation.ID == "" {
		cached.Conversation = event.Conversation
	}
	if cached.Platform == "" {
		cached.Platform = event.Platform
	}
	if cached.Article.ID == "" {
		cached.Article.ID = lookup.ArticleID
	}
	if event.Mutation.After != nil {
		cached.Article.Text = event.Mutation.After.Text
		cached.Article.Entities = append([]otogi.TextEntity(nil), event.Mutation.After.Entities...)
		cached.Article.Media = cloneMediaAttachments(event.Mutation.After.Media)
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
	lookup, err := otogi.ReactionArticleLookupFromEvent(event)
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

	cached := cloneCachedArticle(existing)
	applyReactionToArticle(&cached.Article, *event.Reaction)
	m.upsertEntityLocked(key, cached, now)
	m.mu.Unlock()

	return nil
}

func (m *Module) forgetRetracted(event *otogi.Event) error {
	if event == nil {
		return fmt.Errorf("forget retracted: nil event")
	}
	lookup, err := otogi.MutationArticleLookupFromEvent(event)
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
	lookup, err := otogi.TargetArticleLookupFromEvent(event)
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

func (m *Module) upsertEntityLocked(key cacheKey, cached otogi.CachedArticle, now time.Time) {
	m.upsertKeyLocked(key, now)
	m.entities[key] = cloneCachedArticle(cached)
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
	if event.Kind != otogi.EventKindArticleEdited {
		return event
	}
	if event.Mutation == nil || event.Mutation.Before != nil {
		return event
	}

	existing, exists := m.entities[key]
	if !exists {
		return event
	}
	event.Mutation.Before = &otogi.ArticleSnapshot{
		Text:     existing.Article.Text,
		Entities: append([]otogi.TextEntity(nil), existing.Article.Entities...),
		Media:    cloneMediaAttachments(existing.Article.Media),
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

func cacheKeyFromLookup(lookup otogi.ArticleLookup) cacheKey {
	return cacheKey{
		tenantID:       lookup.TenantID,
		platform:       lookup.Platform,
		conversationID: lookup.ConversationID,
		articleID:      lookup.ArticleID,
	}
}

func normalizeEventTime(occurredAt time.Time, fallback time.Time) time.Time {
	if occurredAt.IsZero() {
		return fallback
	}

	return occurredAt.UTC()
}

func mutationChangedAtOrFallback(mutation *otogi.ArticleMutation, fallback time.Time) time.Time {
	if mutation == nil || mutation.ChangedAt == nil || mutation.ChangedAt.IsZero() {
		return fallback
	}

	return mutation.ChangedAt.UTC()
}

func cloneCachedArticle(cached otogi.CachedArticle) otogi.CachedArticle {
	cloned := cached
	cloned.Article = cloneArticle(cached.Article)

	return cloned
}

func cloneEvent(event otogi.Event) otogi.Event {
	cloned := event
	if event.Article != nil {
		article := cloneArticle(*event.Article)
		cloned.Article = &article
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

func cloneMutation(mutation *otogi.ArticleMutation) *otogi.ArticleMutation {
	if mutation == nil {
		return nil
	}
	cloned := *mutation
	if mutation.ChangedAt != nil {
		changedAt := *mutation.ChangedAt
		cloned.ChangedAt = &changedAt
	}
	if mutation.Before != nil {
		before := cloneArticleSnapshot(*mutation.Before)
		cloned.Before = &before
	}
	if mutation.After != nil {
		after := cloneArticleSnapshot(*mutation.After)
		cloned.After = &after
	}

	return &cloned
}

func cloneArticleSnapshot(snapshot otogi.ArticleSnapshot) otogi.ArticleSnapshot {
	cloned := snapshot
	if len(snapshot.Entities) > 0 {
		cloned.Entities = append([]otogi.TextEntity(nil), snapshot.Entities...)
	} else {
		cloned.Entities = nil
	}
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

func cloneArticle(article otogi.Article) otogi.Article {
	cloned := article
	if len(article.Entities) > 0 {
		cloned.Entities = append([]otogi.TextEntity(nil), article.Entities...)
	} else {
		cloned.Entities = nil
	}
	if len(article.Media) > 0 {
		cloned.Media = cloneMediaAttachments(article.Media)
	} else {
		cloned.Media = nil
	}
	if len(article.Reactions) > 0 {
		cloned.Reactions = cloneArticleReactions(article.Reactions)
	} else {
		cloned.Reactions = nil
	}

	return cloned
}

func cloneArticleReactions(reactions []otogi.ArticleReaction) []otogi.ArticleReaction {
	if len(reactions) == 0 {
		return nil
	}

	cloned := make([]otogi.ArticleReaction, len(reactions))
	copy(cloned, reactions)

	return cloned
}

func applyReactionToArticle(article *otogi.Article, reaction otogi.Reaction) {
	if article == nil {
		return
	}
	if reaction.Emoji == "" {
		return
	}

	index := -1
	for idx := range article.Reactions {
		if article.Reactions[idx].Emoji == reaction.Emoji {
			index = idx
			break
		}
	}

	switch reaction.Action {
	case otogi.ReactionActionAdd:
		if index >= 0 {
			article.Reactions[index].Count++
			return
		}
		article.Reactions = append(article.Reactions, otogi.ArticleReaction{
			Emoji: reaction.Emoji,
			Count: 1,
		})
	case otogi.ReactionActionRemove:
		if index < 0 {
			return
		}
		if article.Reactions[index].Count <= 1 {
			article.Reactions = append(article.Reactions[:index], article.Reactions[index+1:]...)
			return
		}
		article.Reactions[index].Count--
	default:
	}
}

func applyReactionHistoryToArticle(article *otogi.Article, history []otogi.Event, articleID string) {
	if article == nil || len(history) == 0 {
		return
	}

	for _, event := range history {
		if event.Reaction == nil {
			continue
		}
		if event.Kind != otogi.EventKindArticleReactionAdded && event.Kind != otogi.EventKindArticleReactionRemoved {
			continue
		}
		if articleID != "" && event.Reaction.ArticleID != "" && event.Reaction.ArticleID != articleID {
			continue
		}
		applyReactionToArticle(article, *event.Reaction)
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
	_ otogi.ArticleStore    = (*Module)(nil)
)

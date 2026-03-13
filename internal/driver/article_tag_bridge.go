package driver

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"ex-otogi/pkg/otogi/core"
	"ex-otogi/pkg/otogi/platform"
)

const (
	defaultArticleTagBridgeTTL        = 15 * time.Minute
	defaultArticleTagBridgeMaxEntries = 4096
)

type articleTagBridge struct {
	mu         sync.Mutex
	ttl        time.Duration
	maxEntries int
	entries    map[articleTagKey]articleTagEntry
}

type articleTagKey struct {
	sourceID       string
	platform       platform.Platform
	conversationID string
	articleID      string
}

type articleTagEntry struct {
	tags       map[string]string
	recordedAt time.Time
}

func newArticleTagBridge(ttl time.Duration, maxEntries int) *articleTagBridge {
	if ttl <= 0 {
		ttl = defaultArticleTagBridgeTTL
	}
	if maxEntries <= 0 {
		maxEntries = defaultArticleTagBridgeMaxEntries
	}

	return &articleTagBridge{
		ttl:        ttl,
		maxEntries: maxEntries,
		entries:    make(map[articleTagKey]articleTagEntry),
	}
}

// wrapRuntimesWithArticleTags decorates runtimes with a shared bridge that
// remembers framework tags accepted on outbound sends and reattaches them when
// the same driver later publishes the corresponding self-authored article.
//
// This bridge is intentionally best-effort, process-local, and bounded. It
// only correlates outbound sends and later article.created events that share
// the same driver source identity, conversation ID, and article/message ID.
// Modules must tolerate Article.Tags being absent when a runtime cannot satisfy
// those conditions.
func wrapRuntimesWithArticleTags(runtimes []Runtime) []Runtime {
	if len(runtimes) == 0 {
		return nil
	}

	bridge := newArticleTagBridge(defaultArticleTagBridgeTTL, defaultArticleTagBridgeMaxEntries)
	wrapped := make([]Runtime, len(runtimes))
	for index, runtime := range runtimes {
		wrapped[index] = runtime
		if runtime.SinkDispatcher != nil {
			wrapped[index].SinkDispatcher = &articleTagSinkDispatcher{
				source: runtime.Source,
				base:   runtime.SinkDispatcher,
				bridge: bridge,
			}
		}
		if runtime.Driver != nil {
			wrapped[index].Driver = &articleTagDriver{
				source: runtime.Source,
				base:   runtime.Driver,
				bridge: bridge,
			}
		}
	}

	return wrapped
}

type articleTagSinkDispatcher struct {
	source platform.EventSource
	base   platform.SinkDispatcher
	bridge *articleTagBridge
}

func (d *articleTagSinkDispatcher) SendMessage(
	ctx context.Context,
	request platform.SendMessageRequest,
) (*platform.OutboundMessage, error) {
	response, err := d.base.SendMessage(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("article tag sink send message: %w", err)
	}
	if response == nil {
		return nil, nil
	}

	tags := cloneTags(request.Tags)
	if len(tags) > 0 {
		d.bridge.remember(d.source, request.Target.Conversation.ID, response.ID, tags, time.Now().UTC())
	}
	response.Tags = cloneTags(tags)

	return response, nil
}

func (d *articleTagSinkDispatcher) EditMessage(ctx context.Context, request platform.EditMessageRequest) error {
	if err := d.base.EditMessage(ctx, request); err != nil {
		return fmt.Errorf("article tag sink edit message: %w", err)
	}

	return nil
}

func (d *articleTagSinkDispatcher) DeleteMessage(ctx context.Context, request platform.DeleteMessageRequest) error {
	if err := d.base.DeleteMessage(ctx, request); err != nil {
		return fmt.Errorf("article tag sink delete message: %w", err)
	}

	return nil
}

func (d *articleTagSinkDispatcher) SetReaction(ctx context.Context, request platform.SetReactionRequest) error {
	if err := d.base.SetReaction(ctx, request); err != nil {
		return fmt.Errorf("article tag sink set reaction: %w", err)
	}

	return nil
}

func (d *articleTagSinkDispatcher) ListSinks(ctx context.Context) ([]platform.EventSink, error) {
	sinks, err := d.base.ListSinks(ctx)
	if err != nil {
		return nil, fmt.Errorf("article tag sink list sinks: %w", err)
	}

	return sinks, nil
}

func (d *articleTagSinkDispatcher) ListSinksByPlatform(
	ctx context.Context,
	platform platform.Platform,
) ([]platform.EventSink, error) {
	sinks, err := d.base.ListSinksByPlatform(ctx, platform)
	if err != nil {
		return nil, fmt.Errorf("article tag sink list sinks by platform %s: %w", platform, err)
	}

	return sinks, nil
}

type articleTagDriver struct {
	source platform.EventSource
	base   core.Driver
	bridge *articleTagBridge
}

func (d *articleTagDriver) Name() string {
	return d.base.Name()
}

func (d *articleTagDriver) Start(ctx context.Context, dispatcher core.EventDispatcher) error {
	if err := d.base.Start(ctx, &articleTagEventDispatcher{
		source: d.source,
		base:   dispatcher,
		bridge: d.bridge,
	}); err != nil {
		return fmt.Errorf("article tag driver start: %w", err)
	}

	return nil
}

func (d *articleTagDriver) Shutdown(ctx context.Context) error {
	if err := d.base.Shutdown(ctx); err != nil {
		return fmt.Errorf("article tag driver shutdown: %w", err)
	}

	return nil
}

type articleTagEventDispatcher struct {
	source platform.EventSource
	base   core.EventDispatcher
	bridge *articleTagBridge
}

func (d *articleTagEventDispatcher) Publish(ctx context.Context, event *platform.Event) error {
	if event != nil && event.Kind == platform.EventKindArticleCreated && event.Article != nil {
		source := event.Source
		if source.Platform == "" {
			source.Platform = d.source.Platform
		}
		if source.ID == "" {
			source.ID = d.source.ID
		}
		if tags, ok := d.bridge.take(source, event.Conversation.ID, event.Article.ID, time.Now().UTC()); ok {
			event.Article.Tags = mergeTags(event.Article.Tags, tags)
		}
	}

	if err := d.base.Publish(ctx, event); err != nil {
		return fmt.Errorf("article tag publish: %w", err)
	}

	return nil
}

func (b *articleTagBridge) remember(
	source platform.EventSource,
	conversationID string,
	articleID string,
	tags map[string]string,
	now time.Time,
) {
	if b == nil || len(tags) == 0 {
		return
	}

	key, ok := newArticleTagKey(source, conversationID, articleID)
	if !ok {
		return
	}
	recordedAt := normalizeArticleTagTime(now)

	b.mu.Lock()
	defer b.mu.Unlock()

	b.pruneLocked(recordedAt)
	if _, exists := b.entries[key]; !exists && len(b.entries) >= b.maxEntries {
		b.evictOldestLocked()
	}
	b.entries[key] = articleTagEntry{
		tags:       cloneTags(tags),
		recordedAt: recordedAt,
	}
}

func (b *articleTagBridge) take(
	source platform.EventSource,
	conversationID string,
	articleID string,
	now time.Time,
) (map[string]string, bool) {
	if b == nil {
		return nil, false
	}

	key, ok := newArticleTagKey(source, conversationID, articleID)
	if !ok {
		return nil, false
	}
	lookupTime := normalizeArticleTagTime(now)

	b.mu.Lock()
	defer b.mu.Unlock()

	b.pruneLocked(lookupTime)
	entry, exists := b.entries[key]
	if !exists {
		return nil, false
	}
	delete(b.entries, key)

	return cloneTags(entry.tags), true
}

func (b *articleTagBridge) pruneLocked(now time.Time) {
	if len(b.entries) == 0 {
		return
	}

	expiryCutoff := now.Add(-b.ttl)
	for key, entry := range b.entries {
		if entry.recordedAt.Before(expiryCutoff) {
			delete(b.entries, key)
		}
	}
	for len(b.entries) > b.maxEntries {
		b.evictOldestLocked()
	}
}

func (b *articleTagBridge) evictOldestLocked() {
	var (
		oldestKey  articleTagKey
		oldestTime time.Time
		found      bool
	)
	for key, entry := range b.entries {
		if !found || entry.recordedAt.Before(oldestTime) {
			oldestKey = key
			oldestTime = entry.recordedAt
			found = true
		}
	}
	if found {
		delete(b.entries, oldestKey)
	}
}

func newArticleTagKey(
	source platform.EventSource,
	conversationID string,
	articleID string,
) (articleTagKey, bool) {
	trimmedConversationID := strings.TrimSpace(conversationID)
	trimmedArticleID := strings.TrimSpace(articleID)
	if trimmedConversationID == "" || trimmedArticleID == "" {
		return articleTagKey{}, false
	}

	return articleTagKey{
		sourceID:       strings.TrimSpace(source.ID),
		platform:       source.Platform,
		conversationID: trimmedConversationID,
		articleID:      trimmedArticleID,
	}, true
}

func normalizeArticleTagTime(now time.Time) time.Time {
	if now.IsZero() {
		return time.Now().UTC()
	}

	return now.UTC()
}

func cloneTags(tags map[string]string) map[string]string {
	if len(tags) == 0 {
		return nil
	}

	cloned := make(map[string]string, len(tags))
	for key, value := range tags {
		cloned[key] = value
	}

	return cloned
}

func mergeTags(existing map[string]string, added map[string]string) map[string]string {
	switch {
	case len(existing) == 0:
		return cloneTags(added)
	case len(added) == 0:
		return cloneTags(existing)
	}

	merged := cloneTags(existing)
	for key, value := range added {
		if _, exists := merged[key]; exists {
			continue
		}
		merged[key] = value
	}

	return merged
}

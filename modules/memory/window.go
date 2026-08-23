package memory

import (
	"sync"
	"time"
	"unicode/utf8"

	"ex-otogi/pkg/otogi/ai"
	"ex-otogi/pkg/otogi/platform"
)

// bufferedArticle holds one article waiting in the extraction window.
type bufferedArticle struct {
	Article    platform.Article
	Actor      platform.Actor
	OccurredAt time.Time
	ReceivedAt time.Time
}

// FlushReason describes why a window was flushed.
type FlushReason string

const (
	// FlushReasonQuiet means the window was quiet (no new article) for BufferQuietPeriod.
	FlushReasonQuiet FlushReason = "quiet"
	// FlushReasonMaxCount means the article count hit BufferMaxArticles.
	FlushReasonMaxCount FlushReason = "max_count"
	// FlushReasonMaxRunes means the accumulated rune count hit BufferMaxRunes.
	FlushReasonMaxRunes FlushReason = "max_runes"
	// FlushReasonMaxAge means the oldest article exceeded BufferMaxAge.
	FlushReasonMaxAge FlushReason = "max_age"
	// FlushReasonScope means the window was force-drained for consolidation.
	FlushReasonScope FlushReason = "scope_drain"
	// FlushReasonShutdown means the module is shutting down.
	FlushReasonShutdown FlushReason = "shutdown"
)

// readyWindow holds the drained contents of a window that met flush criteria.
type readyWindow struct {
	Scope    ai.SemanticScope
	Articles []bufferedArticle
	Reason   FlushReason
}

type windowSnapshot struct {
	articleCount int
	runeCount    int
	firstRecv    time.Time
	lastRecv     time.Time
}

// articleWindow is the internal per-scope accumulation buffer.
// It is NOT concurrency-safe on its own; the owning windowManager serializes
// all access via its mutex.
type articleWindow struct {
	scope     ai.SemanticScope
	articles  []bufferedArticle
	runes     int
	seen      map[string]struct{}
	firstRecv time.Time
	lastRecv  time.Time
}

// append adds one buffered article to the window, deduplicating by Article.ID.
// It updates the accumulated rune count and the firstRecv/lastRecv timestamps.
func (w *articleWindow) append(a bufferedArticle) {
	if _, exists := w.seen[a.Article.ID]; exists {
		return
	}
	w.seen[a.Article.ID] = struct{}{}
	w.articles = append(w.articles, a)
	w.runes += utf8.RuneCountInString(a.Article.Text)

	if w.firstRecv.IsZero() || a.ReceivedAt.Before(w.firstRecv) {
		w.firstRecv = a.ReceivedAt
	}
	if a.ReceivedAt.After(w.lastRecv) {
		w.lastRecv = a.ReceivedAt
	}
}

// drain returns all buffered articles and resets the window to empty.
func (w *articleWindow) drain() []bufferedArticle {
	out := w.articles
	w.articles = nil
	w.runes = 0
	w.seen = make(map[string]struct{})
	w.firstRecv = time.Time{}
	w.lastRecv = time.Time{}

	return out
}

// empty reports whether the window has no buffered articles.
func (w *articleWindow) empty() bool {
	return len(w.articles) == 0
}

func (w *articleWindow) snapshot() windowSnapshot {
	return windowSnapshot{
		articleCount: len(w.articles),
		runeCount:    w.runes,
		firstRecv:    w.firstRecv,
		lastRecv:     w.lastRecv,
	}
}

// windowScopeKey builds a string key for a memory scope suitable for map
// lookups inside the window manager.
func windowScopeKey(scope ai.SemanticScope) string {
	return scope.TenantID + "\x00" + scope.Platform + "\x00" + scope.ConversationID
}

// windowManager buffers incoming articles per conversation scope and reports
// when a window is ready for extraction based on configurable flush triggers.
type windowManager struct {
	cfg     Config
	clock   func() time.Time
	mu      sync.Mutex
	windows map[string]*articleWindow
}

// newWindowManager creates a window manager with the given config and clock.
func newWindowManager(cfg Config, clock func() time.Time) *windowManager {
	return &windowManager{
		cfg:     cfg,
		clock:   clock,
		windows: make(map[string]*articleWindow),
	}
}

// Enqueue adds one article to the appropriate scope window and returns the
// resulting buffered window snapshot.
// Thread-safe. Deduplicates by Article.ID within a window.
func (m *windowManager) Enqueue(scope ai.SemanticScope, a bufferedArticle) windowSnapshot {
	key := windowScopeKey(scope)

	m.mu.Lock()
	defer m.mu.Unlock()

	w, ok := m.windows[key]
	if !ok {
		w = &articleWindow{
			scope: scope,
			seen:  make(map[string]struct{}),
		}
		m.windows[key] = w
	}

	w.append(a)

	return w.snapshot()
}

// Ready returns all windows that meet any trigger condition, draining them.
// Trigger priority (first match wins per window):
//  1. (now - lastRecv) >= cfg.BufferQuietPeriod  (quiet)
//  2. len(articles) >= cfg.BufferMaxArticles      (max_count)
//  3. runes >= cfg.BufferMaxRunes                  (max_runes)
//  4. (now - firstRecv) >= cfg.BufferMaxAge        (max_age)
//
// Thread-safe.
func (m *windowManager) Ready(now time.Time) []readyWindow {
	m.mu.Lock()
	defer m.mu.Unlock()

	var ready []readyWindow

	for key, w := range m.windows {
		if w.empty() {
			continue
		}

		reason, triggered := m.checkTriggers(now, w)
		if !triggered {
			continue
		}

		ready = append(ready, readyWindow{
			Scope:    w.scope,
			Articles: w.drain(),
			Reason:   reason,
		})
		delete(m.windows, key)
	}

	return ready
}

// checkTriggers evaluates the flush triggers for one window and returns the
// reason and whether the window should be drained. Must be called under mu.
func (m *windowManager) checkTriggers(now time.Time, w *articleWindow) (FlushReason, bool) {
	if now.Sub(w.lastRecv) >= m.cfg.BufferQuietPeriod {
		return FlushReasonQuiet, true
	}
	if len(w.articles) >= m.cfg.BufferMaxArticles {
		return FlushReasonMaxCount, true
	}
	if w.runes >= m.cfg.BufferMaxRunes {
		return FlushReasonMaxRunes, true
	}
	if now.Sub(w.firstRecv) >= m.cfg.BufferMaxAge {
		return FlushReasonMaxAge, true
	}

	return "", false
}

// DrainAll force-drains every non-empty window with FlushReasonShutdown.
// Thread-safe.
func (m *windowManager) DrainAll() []readyWindow {
	m.mu.Lock()
	defer m.mu.Unlock()

	var ready []readyWindow

	for key, w := range m.windows {
		if w.empty() {
			continue
		}
		ready = append(ready, readyWindow{
			Scope:    w.scope,
			Articles: w.drain(),
			Reason:   FlushReasonShutdown,
		})
		delete(m.windows, key)
	}

	return ready
}

// DrainScope force-drains one specific scope with FlushReasonScope.
// Returns ok=false if the scope had no window or was empty.
// Thread-safe.
func (m *windowManager) DrainScope(scope ai.SemanticScope) (readyWindow, bool) {
	key := windowScopeKey(scope)

	m.mu.Lock()
	defer m.mu.Unlock()

	w, exists := m.windows[key]
	if !exists || w.empty() {
		return readyWindow{}, false
	}

	rw := readyWindow{
		Scope:    w.scope,
		Articles: w.drain(),
		Reason:   FlushReasonScope,
	}
	delete(m.windows, key)

	return rw, true
}

// ActiveScopes returns the scope keys of all non-empty windows.
// Used by the consolidation cycle to know which scopes to iterate.
// Thread-safe.
func (m *windowManager) ActiveScopes() []ai.SemanticScope {
	m.mu.Lock()
	defer m.mu.Unlock()

	scopes := make([]ai.SemanticScope, 0, len(m.windows))
	for _, w := range m.windows {
		if w.empty() {
			continue
		}
		scopes = append(scopes, w.scope)
	}

	return scopes
}

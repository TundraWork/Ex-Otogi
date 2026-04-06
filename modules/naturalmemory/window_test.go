package naturalmemory

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"ex-otogi/pkg/otogi/ai"
	"ex-otogi/pkg/otogi/platform"
)

func testWindowConfig() Config {
	return Config{
		BufferQuietPeriod: 2 * time.Minute,
		BufferMaxRunes:    3000,
		BufferMaxArticles: 30,
		BufferMaxAge:      10 * time.Minute,
	}
}

func testScope(id string) ai.LLMMemoryScope {
	return ai.LLMMemoryScope{
		TenantID:       "tenant-1",
		Platform:       "test",
		ConversationID: id,
	}
}

func testBufferedArticle(id, text string, receivedAt time.Time) bufferedArticle {
	return bufferedArticle{
		Article: platform.Article{
			ID:   id,
			Text: text,
		},
		Actor: platform.Actor{
			ID:          "actor-1",
			Username:    "tester",
			DisplayName: "Tester",
		},
		OccurredAt: receivedAt,
		ReceivedAt: receivedAt,
	}
}

func TestWindowManagerEnqueue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		articles     []bufferedArticle
		wantReady    int
		wantBuffered int
	}{
		{
			name: "three articles buffered but not ready before quiet period",
			articles: []bufferedArticle{
				testBufferedArticle("a1", "Hello", time.Unix(100, 0)),
				testBufferedArticle("a2", "World", time.Unix(101, 0)),
				testBufferedArticle("a3", "Again", time.Unix(102, 0)),
			},
			wantReady:    0,
			wantBuffered: 3,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			now := time.Unix(103, 0)
			cfg := testWindowConfig()
			wm := newWindowManager(cfg, func() time.Time { return now })

			scope := testScope("conv-1")
			for _, a := range testCase.articles {
				wm.Enqueue(scope, a)
			}

			ready := wm.Ready(now)
			if len(ready) != testCase.wantReady {
				t.Fatalf("Ready() returned %d windows, want %d", len(ready), testCase.wantReady)
			}

			scopes := wm.ActiveScopes()
			if testCase.wantBuffered > 0 && len(scopes) == 0 {
				t.Fatalf("ActiveScopes() returned 0, want at least 1 active scope")
			}
		})
	}
}

func TestWindowManagerQuietTrigger(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		recvTime   time.Time
		checkTime  time.Time
		wantReady  bool
		wantReason FlushReason
	}{
		{
			name:      "not ready before quiet period elapses",
			recvTime:  time.Unix(100, 0),
			checkTime: time.Unix(100+60, 0), // 1m later, quiet=2m
			wantReady: false,
		},
		{
			name:       "ready after quiet period elapses",
			recvTime:   time.Unix(100, 0),
			checkTime:  time.Unix(100+121, 0), // 2m1s later
			wantReady:  true,
			wantReason: FlushReasonQuiet,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			cfg := testWindowConfig()
			wm := newWindowManager(cfg, func() time.Time { return testCase.checkTime })

			scope := testScope("conv-1")
			wm.Enqueue(scope, testBufferedArticle("a1", "Hello world", testCase.recvTime))

			ready := wm.Ready(testCase.checkTime)
			if testCase.wantReady {
				if len(ready) != 1 {
					t.Fatalf("Ready() returned %d windows, want 1", len(ready))
				}
				if ready[0].Reason != testCase.wantReason {
					t.Fatalf("Reason = %q, want %q", ready[0].Reason, testCase.wantReason)
				}
				if len(ready[0].Articles) != 1 {
					t.Fatalf("Articles len = %d, want 1", len(ready[0].Articles))
				}
			} else {
				if len(ready) != 0 {
					t.Fatalf("Ready() returned %d windows, want 0", len(ready))
				}
			}
		})
	}
}

func TestWindowManagerMaxCountTrigger(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		count      int
		wantReady  bool
		wantReason FlushReason
	}{
		{
			name:      "below max count is not ready",
			count:     29,
			wantReady: false,
		},
		{
			name:       "at max count triggers flush",
			count:      30,
			wantReady:  true,
			wantReason: FlushReasonMaxCount,
		},
		{
			name:       "above max count triggers flush",
			count:      35,
			wantReady:  true,
			wantReason: FlushReasonMaxCount,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			// Use a recent receivedAt so quiet trigger does not fire.
			now := time.Unix(1000, 0)
			cfg := testWindowConfig()
			wm := newWindowManager(cfg, func() time.Time { return now })

			scope := testScope("conv-1")
			for i := range testCase.count {
				a := testBufferedArticle(
					fmt.Sprintf("a%d", i),
					"hi",
					now.Add(-time.Duration(testCase.count-i)*time.Second),
				)
				wm.Enqueue(scope, a)
			}

			ready := wm.Ready(now)
			if testCase.wantReady {
				if len(ready) != 1 {
					t.Fatalf("Ready() returned %d windows, want 1", len(ready))
				}
				if ready[0].Reason != testCase.wantReason {
					t.Fatalf("Reason = %q, want %q", ready[0].Reason, testCase.wantReason)
				}
				if len(ready[0].Articles) != testCase.count {
					t.Fatalf("Articles len = %d, want %d", len(ready[0].Articles), testCase.count)
				}
			} else {
				if len(ready) != 0 {
					t.Fatalf("Ready() returned %d windows, want 0", len(ready))
				}
			}
		})
	}
}

func TestWindowManagerMaxRunesTrigger(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		textRunes  int
		wantReady  bool
		wantReason FlushReason
	}{
		{
			name:      "below max runes is not ready",
			textRunes: 2999,
			wantReady: false,
		},
		{
			name:       "at max runes triggers flush",
			textRunes:  3000,
			wantReady:  true,
			wantReason: FlushReasonMaxRunes,
		},
		{
			name:       "above max runes triggers flush",
			textRunes:  4000,
			wantReady:  true,
			wantReason: FlushReasonMaxRunes,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			now := time.Unix(1000, 0)
			cfg := testWindowConfig()
			wm := newWindowManager(cfg, func() time.Time { return now })

			scope := testScope("conv-1")
			text := strings.Repeat("x", testCase.textRunes)
			a := testBufferedArticle("a1", text, now.Add(-10*time.Second))
			wm.Enqueue(scope, a)

			ready := wm.Ready(now)
			if testCase.wantReady {
				if len(ready) != 1 {
					t.Fatalf("Ready() returned %d windows, want 1", len(ready))
				}
				if ready[0].Reason != testCase.wantReason {
					t.Fatalf("Reason = %q, want %q", ready[0].Reason, testCase.wantReason)
				}
			} else {
				if len(ready) != 0 {
					t.Fatalf("Ready() returned %d windows, want 0", len(ready))
				}
			}
		})
	}
}

func TestWindowManagerMaxAgeTrigger(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		firstRecv  time.Time
		checkTime  time.Time
		wantReady  bool
		wantReason FlushReason
	}{
		{
			name:      "not ready before max age",
			firstRecv: time.Unix(100, 0),
			checkTime: time.Unix(100+500, 0), // 8m20s, max_age=10m
			wantReady: false,
		},
		{
			name:       "ready after max age",
			firstRecv:  time.Unix(100, 0),
			checkTime:  time.Unix(100+601, 0), // 10m1s
			wantReady:  true,
			wantReason: FlushReasonMaxAge,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			cfg := testWindowConfig()
			wm := newWindowManager(cfg, func() time.Time { return testCase.checkTime })

			scope := testScope("conv-1")
			// First article at firstRecv.
			wm.Enqueue(scope, testBufferedArticle("a1", "Hello", testCase.firstRecv))
			// Second article just before checkTime so quiet trigger does not fire.
			wm.Enqueue(scope, testBufferedArticle("a2", "World", testCase.checkTime.Add(-30*time.Second)))

			ready := wm.Ready(testCase.checkTime)
			if testCase.wantReady {
				if len(ready) != 1 {
					t.Fatalf("Ready() returned %d windows, want 1", len(ready))
				}
				if ready[0].Reason != testCase.wantReason {
					t.Fatalf("Reason = %q, want %q", ready[0].Reason, testCase.wantReason)
				}
				if len(ready[0].Articles) != 2 {
					t.Fatalf("Articles len = %d, want 2", len(ready[0].Articles))
				}
			} else {
				if len(ready) != 0 {
					t.Fatalf("Ready() returned %d windows, want 0", len(ready))
				}
			}
		})
	}
}

func TestWindowManagerDedup(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		articleIDs   []string
		wantArticles int
	}{
		{
			name:         "duplicate article ID is ignored",
			articleIDs:   []string{"a1", "a1"},
			wantArticles: 1,
		},
		{
			name:         "distinct IDs are all kept",
			articleIDs:   []string{"a1", "a2", "a3"},
			wantArticles: 3,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			cfg := testWindowConfig()
			now := time.Unix(1000, 0)
			wm := newWindowManager(cfg, func() time.Time { return now })

			scope := testScope("conv-1")
			for _, id := range testCase.articleIDs {
				wm.Enqueue(scope, testBufferedArticle(id, "some text", now.Add(-10*time.Second)))
			}

			// Force-drain to inspect articles.
			rw, ok := wm.DrainScope(scope)
			if !ok {
				t.Fatal("DrainScope returned ok=false, expected articles")
			}
			if len(rw.Articles) != testCase.wantArticles {
				t.Fatalf("Articles len = %d, want %d", len(rw.Articles), testCase.wantArticles)
			}
		})
	}
}

func TestWindowManagerDrainAll(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		scopes     []string
		wantDrain  int
		wantReason FlushReason
	}{
		{
			name:       "drains all non-empty scopes",
			scopes:     []string{"conv-1", "conv-2", "conv-3"},
			wantDrain:  3,
			wantReason: FlushReasonShutdown,
		},
		{
			name:      "no scopes returns empty",
			scopes:    nil,
			wantDrain: 0,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			cfg := testWindowConfig()
			now := time.Unix(1000, 0)
			wm := newWindowManager(cfg, func() time.Time { return now })

			for _, convID := range testCase.scopes {
				scope := testScope(convID)
				wm.Enqueue(scope, testBufferedArticle("a-"+convID, "text", now))
			}

			drained := wm.DrainAll()
			if len(drained) != testCase.wantDrain {
				t.Fatalf("DrainAll() returned %d windows, want %d", len(drained), testCase.wantDrain)
			}
			for _, rw := range drained {
				if rw.Reason != testCase.wantReason {
					t.Fatalf("Reason = %q, want %q", rw.Reason, testCase.wantReason)
				}
				if len(rw.Articles) == 0 {
					t.Fatal("drained window has 0 articles")
				}
			}

			// After drain, no active scopes remain.
			if scopes := wm.ActiveScopes(); len(scopes) != 0 {
				t.Fatalf("ActiveScopes() after DrainAll = %d, want 0", len(scopes))
			}
		})
	}
}

func TestWindowManagerDrainScope(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		drainConv  string
		wantOK     bool
		wantReason FlushReason
		wantRemain int
	}{
		{
			name:       "drains only the requested scope",
			drainConv:  "conv-1",
			wantOK:     true,
			wantReason: FlushReasonScope,
			wantRemain: 1, // conv-2 remains
		},
		{
			name:       "missing scope returns false",
			drainConv:  "conv-missing",
			wantOK:     false,
			wantRemain: 2, // both remain
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			cfg := testWindowConfig()
			now := time.Unix(1000, 0)
			wm := newWindowManager(cfg, func() time.Time { return now })

			scope1 := testScope("conv-1")
			scope2 := testScope("conv-2")
			wm.Enqueue(scope1, testBufferedArticle("a1", "text-1", now))
			wm.Enqueue(scope2, testBufferedArticle("a2", "text-2", now))

			drainScope := testScope(testCase.drainConv)
			rw, ok := wm.DrainScope(drainScope)

			if ok != testCase.wantOK {
				t.Fatalf("DrainScope ok = %v, want %v", ok, testCase.wantOK)
			}
			if testCase.wantOK {
				if rw.Reason != testCase.wantReason {
					t.Fatalf("Reason = %q, want %q", rw.Reason, testCase.wantReason)
				}
				if len(rw.Articles) == 0 {
					t.Fatal("drained window has 0 articles")
				}
			}

			remaining := wm.ActiveScopes()
			if len(remaining) != testCase.wantRemain {
				t.Fatalf("ActiveScopes() after DrainScope = %d, want %d", len(remaining), testCase.wantRemain)
			}
		})
	}
}

func TestWindowManagerDrainedWindowRemoved(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		firstText   string
		secondText  string
		wantArticle string
	}{
		{
			name:        "fresh window after drain contains only new article",
			firstText:   "old message",
			secondText:  "new message",
			wantArticle: "a2",
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			cfg := testWindowConfig()
			now := time.Unix(1000, 0)
			wm := newWindowManager(cfg, func() time.Time { return now })

			scope := testScope("conv-1")
			wm.Enqueue(scope, testBufferedArticle("a1", testCase.firstText, now))

			// Drain the scope.
			_, ok := wm.DrainScope(scope)
			if !ok {
				t.Fatal("DrainScope returned ok=false on non-empty window")
			}

			// Enqueue a new article to the same scope.
			wm.Enqueue(scope, testBufferedArticle(testCase.wantArticle, testCase.secondText, now.Add(time.Second)))

			// Drain again; should contain only the new article.
			rw, ok := wm.DrainScope(scope)
			if !ok {
				t.Fatal("DrainScope returned ok=false after re-enqueue")
			}
			if len(rw.Articles) != 1 {
				t.Fatalf("Articles len = %d, want 1", len(rw.Articles))
			}
			if rw.Articles[0].Article.ID != testCase.wantArticle {
				t.Fatalf("Article.ID = %q, want %q", rw.Articles[0].Article.ID, testCase.wantArticle)
			}
		})
	}
}

func TestWindowManagerActiveScopes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		convIDs   []string
		wantCount int
	}{
		{
			name:      "returns all active scopes",
			convIDs:   []string{"conv-1", "conv-2", "conv-3"},
			wantCount: 3,
		},
		{
			name:      "returns empty when no scopes",
			convIDs:   nil,
			wantCount: 0,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			cfg := testWindowConfig()
			now := time.Unix(1000, 0)
			wm := newWindowManager(cfg, func() time.Time { return now })

			for _, convID := range testCase.convIDs {
				scope := testScope(convID)
				wm.Enqueue(scope, testBufferedArticle("a-"+convID, "text", now))
			}

			scopes := wm.ActiveScopes()
			if len(scopes) != testCase.wantCount {
				t.Fatalf("ActiveScopes() len = %d, want %d", len(scopes), testCase.wantCount)
			}

			// Verify all expected conversation IDs are present.
			found := make(map[string]bool, len(scopes))
			for _, s := range scopes {
				found[s.ConversationID] = true
			}
			for _, convID := range testCase.convIDs {
				if !found[convID] {
					t.Fatalf("ActiveScopes() missing ConversationID %q", convID)
				}
			}
		})
	}
}

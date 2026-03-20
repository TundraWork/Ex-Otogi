package naturalmemory

import (
	"testing"
	"time"

	"ex-otogi/pkg/otogi/ai"
)

func TestEffectiveScoreDecay(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 16, 12, 0, 0, 0, time.UTC)
	fresh := ai.LLMMemoryRecord{
		ID:        "fresh",
		CreatedAt: now.Add(-1 * time.Hour),
		Metadata: map[string]string{
			"importance":    "5",
			"last_accessed": now.Add(-1 * time.Hour).Format(time.RFC3339),
		},
	}
	stale := ai.LLMMemoryRecord{
		ID:        "stale",
		CreatedAt: now.Add(-10 * time.Hour),
		Metadata: map[string]string{
			"importance":    "5",
			"last_accessed": now.Add(-10 * time.Hour).Format(time.RFC3339),
		},
	}

	if gotFresh, gotStale := effectiveScore(fresh, 0.9, now), effectiveScore(stale, 0.9, now); gotFresh <= gotStale {
		t.Fatalf("fresh score = %f, stale score = %f, want fresh > stale", gotFresh, gotStale)
	}
}

func TestRankMemories(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 16, 12, 0, 0, 0, time.UTC)
	matches := []ai.LLMMemoryMatch{
		{
			Record: ai.LLMMemoryRecord{
				ID:        "older-low",
				CreatedAt: now.Add(-4 * time.Hour),
				Metadata: map[string]string{
					"importance":    "3",
					"last_accessed": now.Add(-4 * time.Hour).Format(time.RFC3339),
				},
			},
			Similarity: 0.95,
		},
		{
			Record: ai.LLMMemoryRecord{
				ID:        "recent-high",
				CreatedAt: now.Add(-30 * time.Minute),
				Metadata: map[string]string{
					"importance":    "8",
					"last_accessed": now.Add(-30 * time.Minute).Format(time.RFC3339),
				},
			},
			Similarity: 0.8,
		},
	}

	ranked := RankMemories(matches, 0.95, now)
	if len(ranked) != 2 {
		t.Fatalf("ranked len = %d, want 2", len(ranked))
	}
	if ranked[0].Record.ID != "recent-high" {
		t.Fatalf("ranked[0] = %q, want recent-high", ranked[0].Record.ID)
	}
}

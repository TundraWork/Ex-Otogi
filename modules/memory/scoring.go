package memory

import (
	"context"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"ex-otogi/pkg/otogi/ai"
)

// RankMemories reorders semantic matches by combining similarity, importance,
// and recency decay.
func RankMemories(matches []ai.SemanticMatch, decayFactor float64, now time.Time) []ai.SemanticMatch {
	type scoredMatch struct {
		match      ai.SemanticMatch
		finalScore float64
	}

	scored := make([]scoredMatch, 0, len(matches))
	for _, match := range matches {
		importance := memoryImportance(match.Record)
		lastAccessed := memoryLastAccessed(match.Record)

		hoursSinceAccess := now.Sub(lastAccessed).Hours()
		importanceWeight := 0.5 + (float64(importance) / 20.0)
		recencyWeight := math.Pow(decayFactor, hoursSinceAccess)
		finalScore := float64(match.Similarity) * importanceWeight * recencyWeight

		scored = append(scored, scoredMatch{
			match:      match,
			finalScore: finalScore,
		})
	}

	sort.Slice(scored, func(i, j int) bool {
		if scored[i].finalScore == scored[j].finalScore {
			return scored[i].match.Record.CreatedAt.After(scored[j].match.Record.CreatedAt)
		}
		return scored[i].finalScore > scored[j].finalScore
	})

	ranked := make([]ai.SemanticMatch, len(scored))
	for index, entry := range scored {
		ranked[index] = entry.match
	}

	return ranked
}

// ReinforceMemoryAccess is a placeholder until SemanticStore grows
// metadata-only updates. The current interface cannot persist access metadata
// mutations without rewriting content and embeddings, so reinforcement is a
// no-op for now.
func ReinforceMemoryAccess(context.Context, ai.SemanticStore, ai.SemanticRecord) error {
	return nil
}

func effectiveScore(record ai.SemanticRecord, decayFactor float64, now time.Time) float64 {
	importance := memoryImportance(record)
	lastAccessed := memoryLastAccessed(record)

	return float64(importance) * math.Pow(decayFactor, now.Sub(lastAccessed).Hours())
}

func memoryImportance(record ai.SemanticRecord) int {
	if record.Profile.Importance > 0 {
		return record.Profile.Importance
	}

	return parseMetadataInt(record.Metadata, ai.SemanticMetadataImportance, 5)
}

func memoryLastAccessed(record ai.SemanticRecord) time.Time {
	if !record.Profile.LastAccessedAt.IsZero() {
		return record.Profile.LastAccessedAt.UTC()
	}

	return parseMetadataTime(record.Metadata, ai.SemanticMetadataLastAccessed, record.CreatedAt)
}

func parseMetadataInt(metadata map[string]string, key string, defaultValue int) int {
	if len(metadata) == 0 {
		return defaultValue
	}

	raw := strings.TrimSpace(metadata[key])
	if raw == "" {
		return defaultValue
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return defaultValue
	}

	return value
}

func parseMetadataTime(metadata map[string]string, key string, defaultValue time.Time) time.Time {
	if len(metadata) == 0 {
		return defaultValue
	}

	raw := strings.TrimSpace(metadata[key])
	if raw == "" {
		return defaultValue
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return defaultValue
	}

	return parsed.UTC()
}

func sortMemoriesByScore(records []ai.SemanticRecord, decayFactor float64, now time.Time) {
	sort.Slice(records, func(i, j int) bool {
		leftScore := effectiveScore(records[i], decayFactor, now)
		rightScore := effectiveScore(records[j], decayFactor, now)
		if leftScore == rightScore {
			return records[i].UpdatedAt.After(records[j].UpdatedAt)
		}
		return leftScore > rightScore
	})
}

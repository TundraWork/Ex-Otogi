package naturalmemory

import (
	"strings"
	"unicode"

	"ex-otogi/pkg/otogi/ai"
)

type dedupAction string

const (
	dedupActionAdd    dedupAction = "add"
	dedupActionUpdate dedupAction = "update"
	dedupActionNoop   dedupAction = "noop"
)

func classifyCandidate(
	candidate extractedMemory,
	candidateEmbedding []float32,
	existingMatches []ai.LLMMemoryMatch,
	threshold float32,
) (dedupAction, string) {
	_ = candidateEmbedding
	if len(existingMatches) == 0 {
		return dedupActionAdd, ""
	}

	var bestMatch *ai.LLMMemoryMatch
	for index := range existingMatches {
		match := &existingMatches[index]
		if match.Similarity < threshold {
			continue
		}
		if bestMatch == nil || match.Similarity > bestMatch.Similarity {
			bestMatch = match
		}
	}
	if bestMatch == nil {
		return dedupActionAdd, ""
	}
	if isSubstantivelyDifferent(candidate.Content, bestMatch.Record.Content) {
		return dedupActionUpdate, bestMatch.Record.ID
	}

	return dedupActionNoop, ""
}

func isSubstantivelyDifferent(left string, right string) bool {
	leftNorm := normalizeComparisonText(left)
	rightNorm := normalizeComparisonText(right)
	switch {
	case leftNorm == rightNorm:
		return false
	case leftNorm == "" || rightNorm == "":
		return leftNorm != rightNorm
	case strings.Contains(leftNorm, rightNorm), strings.Contains(rightNorm, leftNorm):
		return true
	}

	return tokenJaccard(leftNorm, rightNorm) < 0.9
}

func normalizeComparisonText(value string) string {
	var builder strings.Builder
	builder.Grow(len(value))

	spacePending := false
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			if spacePending && builder.Len() > 0 {
				builder.WriteByte(' ')
			}
			builder.WriteRune(r)
			spacePending = false
			continue
		}
		spacePending = true
	}

	return builder.String()
}

func tokenJaccard(left string, right string) float64 {
	leftTokens := uniqueTokens(left)
	rightTokens := uniqueTokens(right)
	if len(leftTokens) == 0 || len(rightTokens) == 0 {
		return 0
	}

	intersection := 0
	union := len(leftTokens)
	for token := range rightTokens {
		if _, exists := leftTokens[token]; exists {
			intersection++
			continue
		}
		union++
	}
	if union == 0 {
		return 1
	}

	return float64(intersection) / float64(union)
}

func uniqueTokens(value string) map[string]struct{} {
	tokens := strings.Fields(value)
	set := make(map[string]struct{}, len(tokens))
	for _, token := range tokens {
		set[token] = struct{}{}
	}

	return set
}

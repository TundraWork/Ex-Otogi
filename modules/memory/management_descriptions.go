package memory

import (
	"fmt"
	"strings"

	panel "ex-otogi/pkg/otogi/management"
)

func joinManagementDescriptionParts(parts ...string) string {
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			continue
		}
		filtered = append(filtered, part)
	}
	if len(filtered) == 0 {
		return ""
	}

	return panel.TruncateDescription(strings.Join(filtered, "; "))
}

func latestWindowPreview(articles []bufferedArticle) string {
	for index := len(articles) - 1; index >= 0; index-- {
		if strings.TrimSpace(articles[index].Article.Text) != "" {
			return articles[index].Article.Text
		}
	}

	return ""
}

func extractCompletedDescription(extractedCount int, appliedCount int, failedCount int) string {
	return joinManagementDescriptionParts(
		fmt.Sprintf("%d extracted", extractedCount),
		fmt.Sprintf("%d applied", appliedCount),
		fmt.Sprintf("%d failed", failedCount),
	)
}

func extractApplyFailureDescription(candidate extractedMemory, err error) string {
	return joinManagementDescriptionParts(candidateDescription(candidate), errorDescription(err))
}

func extractParseFailureDescription(err error, responseText string) string {
	return joinManagementDescriptionParts(errorDescription(err), responseText)
}

func processingFailureDescription(reason string, articles []bufferedArticle, err error) string {
	return joinManagementDescriptionParts(
		fmt.Sprintf("%s after %d articles", reason, len(articles)),
		latestWindowPreview(articles),
		errorDescription(err),
	)
}

func consolidationPrunedDescription(expiredCount int, prunedCount int, keptCount int) string {
	return joinManagementDescriptionParts(
		fmt.Sprintf("%d expired", expiredCount),
		fmt.Sprintf("%d low-score pruned", prunedCount),
		fmt.Sprintf("%d kept", keptCount),
	)
}

func consolidationCycleDescription(scopeCount int, elapsedMS int64) string {
	return panel.TruncateDescription(fmt.Sprintf("%d scopes in %dms", scopeCount, elapsedMS))
}

func errorDescription(err error) string {
	if err == nil {
		return ""
	}

	return err.Error()
}

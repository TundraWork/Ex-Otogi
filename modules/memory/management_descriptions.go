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

func windowEnqueuedDescription(text string, articleCount int) string {
	return joinManagementDescriptionParts(fmt.Sprintf("window=%d", articleCount), text)
}

func windowFlushedDescription(reason FlushReason, articles []bufferedArticle) string {
	return joinManagementDescriptionParts(
		fmt.Sprintf("%s after %d articles", reason, len(articles)),
		latestWindowPreview(articles),
	)
}

func windowSkippedDescription(reason string, articles []bufferedArticle) string {
	return joinManagementDescriptionParts(
		fmt.Sprintf("%s after %d articles", reason, len(articles)),
		latestWindowPreview(articles),
	)
}

func extractStartedDescription(previewText string, existingCount int) string {
	return joinManagementDescriptionParts(
		fmt.Sprintf("%d existing memories", existingCount),
		previewText,
	)
}

func extractCompletedDescription(candidates []extractedMemory, appliedCount int, failedCount int) string {
	parts := []string{
		fmt.Sprintf("%d extracted", len(candidates)),
		fmt.Sprintf("%d applied", appliedCount),
		fmt.Sprintf("%d failed", failedCount),
	}
	previews := make([]string, 0, 2)
	for _, candidate := range candidates {
		description := candidateDescription(candidate)
		if strings.TrimSpace(description) == "" {
			continue
		}
		previews = append(previews, description)
		if len(previews) == 2 {
			break
		}
	}
	if len(previews) > 0 {
		parts = append(parts, strings.Join(previews, " | "))
	}

	return joinManagementDescriptionParts(parts...)
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

func consolidationCappedDescription(overflow int, maxAllowed int, removedPreview string) string {
	return joinManagementDescriptionParts(
		fmt.Sprintf("removed %d to enforce cap %d", overflow, maxAllowed),
		removedPreview,
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

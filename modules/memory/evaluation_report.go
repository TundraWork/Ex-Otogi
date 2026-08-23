package memory

import (
	"fmt"
	"strings"
)

const (
	maxEvaluationQualityRegression = 0.02
	minEvaluationCallReduction     = 0.20
)

// EvaluationComparison records whether a candidate satisfies the architecture
// thresholds relative to a checked-in baseline.
type EvaluationComparison struct {
	Accepted                  bool     `json:"accepted"`
	FormationF1Regression     float64  `json:"formation_f1_regression"`
	NDCGAt5Regression         float64  `json:"ndcg_at_5_regression"`
	ProviderCallReduction     float64  `json:"provider_call_reduction"`
	FormationP95Reduction     float64  `json:"formation_p95_reduction"`
	RetrievalP95Reduction     float64  `json:"retrieval_p95_reduction"`
	ProhibitedMemoryRateDelta float64  `json:"prohibited_memory_rate_delta"`
	Violations                []string `json:"violations,omitempty"`
}

// CompareEvaluationReports applies the architecture's quality and cost
// acceptance rule to one candidate report.
func CompareEvaluationReports(baseline, candidate EvaluationReport, requireCostReduction ...bool) EvaluationComparison {
	comparison := EvaluationComparison{
		FormationF1Regression:     relativeRegression(baseline.Metrics.FormationF1, candidate.Metrics.FormationF1),
		NDCGAt5Regression:         relativeRegression(baseline.Metrics.NDCGAt5, candidate.Metrics.NDCGAt5),
		ProviderCallReduction:     relativeReduction(float64(totalEvaluationProviderCalls(baseline.Metrics)), float64(totalEvaluationProviderCalls(candidate.Metrics))),
		FormationP95Reduction:     relativeReduction(baseline.Metrics.FormationP95MS, candidate.Metrics.FormationP95MS),
		RetrievalP95Reduction:     relativeReduction(baseline.Metrics.RetrievalP95MS, candidate.Metrics.RetrievalP95MS),
		ProhibitedMemoryRateDelta: candidate.Metrics.ProhibitedMemoryRate - baseline.Metrics.ProhibitedMemoryRate,
	}
	if comparison.FormationF1Regression > maxEvaluationQualityRegression {
		comparison.Violations = append(comparison.Violations, fmt.Sprintf("formation F1 regressed %.2f%% (maximum 2%%)", comparison.FormationF1Regression*100))
	}
	if comparison.NDCGAt5Regression > maxEvaluationQualityRegression {
		comparison.Violations = append(comparison.Violations, fmt.Sprintf("nDCG@5 regressed %.2f%% (maximum 2%%)", comparison.NDCGAt5Regression*100))
	}
	if comparison.ProhibitedMemoryRateDelta > 0 {
		comparison.Violations = append(comparison.Violations, "prohibited-memory rate increased")
	}
	if len(requireCostReduction) > 0 && requireCostReduction[0] && comparison.ProviderCallReduction < minEvaluationCallReduction {
		comparison.Violations = append(comparison.Violations, "candidate did not reduce deterministic provider calls by 20%; latency is report-only")
	}
	comparison.Accepted = len(comparison.Violations) == 0
	return comparison
}

// RenderEvaluationMarkdown renders a compact human-readable evaluation report.
func RenderEvaluationMarkdown(report EvaluationReport, comparison *EvaluationComparison) string {
	m := report.Metrics
	var builder strings.Builder
	fmt.Fprintf(&builder, "# Memory Evaluation Report\n\nCorpus version: %d  \nRetrieval planner: %t\n\n", report.CorpusVersion, report.RetrievalPlanningEnabled)
	builder.WriteString("## Quality\n\n| Metric | Value |\n|---|---:|\n")
	fmt.Fprintf(&builder, "| Formation precision | %.4f |\n| Formation recall | %.4f |\n| Formation F1 | %.4f |\n| Supersession correctness | %.4f |\n| Duplicate rate | %.4f |\n| Prohibited-memory rate | %.4f |\n| Recall@5 | %.4f |\n| MRR | %.4f |\n| nDCG@5 | %.4f |\n", m.FormationPrecision, m.FormationRecall, m.FormationF1, m.SupersessionCorrectness, m.DuplicateRate, m.ProhibitedMemoryRate, m.RecallAt5, m.MRR, m.NDCGAt5)
	builder.WriteString("\n## Cost and latency\n\n| Metric | Value |\n|---|---:|\n")
	fmt.Fprintf(&builder, "| Formation p50 / p95 | %.3f ms / %.3f ms |\n| Retrieval p50 / p95 | %.3f ms / %.3f ms |\n| Extraction calls / 100 articles | %.2f |\n| Planning calls / chat | %.2f |\n| Formation embedding calls / 100 articles | %.2f |\n| Retrieval embedding calls / chat | %.2f |\n| Consolidation calls / 100 articles | %.2f |\n| Stored records / 1,000 articles | %.2f |\n| Stored bytes / 1,000 articles | %.2f |\n", m.FormationP50MS, m.FormationP95MS, m.RetrievalP50MS, m.RetrievalP95MS, m.ExtractionCallsPer100, m.PlanningCallsPerChat, m.FormationEmbeddingCallsPer100, m.RetrievalEmbeddingCallsPerChat, m.ConsolidationCallsPer100, m.StoredRecordsPer1000, m.StoredBytesPer1000)
	if comparison != nil {
		builder.WriteString("\n## Baseline comparison\n\n")
		fmt.Fprintf(&builder, "Accepted: **%t**  \nProvider-call reduction: %.2f%%  \nFormation F1 regression: %.2f%%  \nnDCG@5 regression: %.2f%%\n", comparison.Accepted, comparison.ProviderCallReduction*100, comparison.FormationF1Regression*100, comparison.NDCGAt5Regression*100)
		if len(comparison.Violations) > 0 {
			builder.WriteString("\nViolations:\n\n")
			for _, violation := range comparison.Violations {
				fmt.Fprintf(&builder, "- %s\n", violation)
			}
		}
	}
	return builder.String()
}

func totalEvaluationProviderCalls(metrics EvaluationMetrics) int {
	return metrics.ExtractionCallCount + metrics.PlanningCallCount + metrics.FormationEmbeddingCallCount + metrics.RetrievalEmbeddingCallCount + metrics.ConsolidationCallCount
}

func relativeRegression(baseline, candidate float64) float64 {
	if baseline <= 0 {
		if candidate < baseline {
			return 1
		}
		return 0
	}
	return max((baseline-candidate)/baseline, 0)
}

func relativeReduction(baseline, candidate float64) float64 {
	if baseline <= 0 {
		return 0
	}
	return (baseline - candidate) / baseline
}

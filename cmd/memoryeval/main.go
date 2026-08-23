// Command memoryeval runs the deterministic production-path memory benchmark.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"ex-otogi/modules/memory"
	"ex-otogi/modules/semanticstore"
	"ex-otogi/pkg/otogi/ai"
)

const defaultCorpusPath = "modules/memory/testdata/evaluation/corpus.json"

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("memoryeval", flag.ContinueOnError)
	flags.SetOutput(stderr)
	corpusPath := flags.String("corpus", defaultCorpusPath, "path to the evaluation corpus")
	jsonPath := flags.String("json", "-", "JSON report path, or - for stdout")
	markdownPath := flags.String("markdown", "", "optional Markdown report path")
	baselinePath := flags.String("baseline", "", "optional baseline JSON report to compare against")
	plannerEnabled := flags.Bool("planner", true, "enable LLM retrieval planning")
	requireCostReduction := flags.Bool("require-cost-reduction", false, "require a 20% provider-call reduction for mechanism-removal candidates")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("parse memory evaluation flags: %w", err)
	}

	corpusFile, err := os.Open(filepath.Clean(*corpusPath))
	if err != nil {
		return fmt.Errorf("open memory evaluation corpus: %w", err)
	}
	corpus, loadErr := memory.LoadEvaluationCorpus(corpusFile)
	closeErr := corpusFile.Close()
	if loadErr != nil {
		return fmt.Errorf("load memory evaluation corpus: %w", loadErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close memory evaluation corpus: %w", closeErr)
	}

	report, err := memory.RunEvaluation(ctx, corpus, memory.EvaluationOptions{
		RetrievalPlanningEnabled: *plannerEnabled,
		StoreFactory: func() ai.SemanticStore {
			return semanticstore.New()
		},
	})
	if err != nil {
		return fmt.Errorf("run memory evaluation: %w", err)
	}

	var comparison *memory.EvaluationComparison
	if strings.TrimSpace(*baselinePath) != "" {
		baseline, baselineErr := loadReport(*baselinePath)
		if baselineErr != nil {
			return baselineErr
		}
		result := memory.CompareEvaluationReports(baseline, report, *requireCostReduction)
		comparison = &result
		report.Comparison = comparison
	}

	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("encode memory evaluation report: %w", err)
	}
	encoded = append(encoded, '\n')
	if err := writeOutput(*jsonPath, encoded, stdout); err != nil {
		return err
	}
	if strings.TrimSpace(*markdownPath) != "" {
		if err := writeOutput(*markdownPath, []byte(memory.RenderEvaluationMarkdown(report, comparison)), stdout); err != nil {
			return err
		}
	}
	if comparison != nil && !comparison.Accepted {
		return fmt.Errorf("memory evaluation candidate rejected: %s", strings.Join(comparison.Violations, "; "))
	}
	return nil
}

func loadReport(path string) (memory.EvaluationReport, error) {
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return memory.EvaluationReport{}, fmt.Errorf("open memory evaluation baseline: %w", err)
	}
	var report memory.EvaluationReport
	decodeErr := json.NewDecoder(file).Decode(&report)
	closeErr := file.Close()
	if decodeErr != nil {
		return memory.EvaluationReport{}, fmt.Errorf("decode memory evaluation baseline: %w", decodeErr)
	}
	if closeErr != nil {
		return memory.EvaluationReport{}, fmt.Errorf("close memory evaluation baseline: %w", closeErr)
	}
	return report, nil
}

func writeOutput(path string, content []byte, stdout io.Writer) error {
	if path == "-" {
		if _, err := stdout.Write(content); err != nil {
			return fmt.Errorf("write memory evaluation output: %w", err)
		}
		return nil
	}
	cleanPath := filepath.Clean(path)
	if err := os.MkdirAll(filepath.Dir(cleanPath), 0o755); err != nil {
		return fmt.Errorf("create memory evaluation output directory: %w", err)
	}
	if err := os.WriteFile(cleanPath, content, 0o600); err != nil {
		return fmt.Errorf("write memory evaluation output %s: %w", cleanPath, err)
	}
	return nil
}

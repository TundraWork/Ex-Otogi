package memory_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"ex-otogi/modules/memory"
	"ex-otogi/modules/semanticstore"
	"ex-otogi/pkg/otogi/ai"
)

func TestLoadEvaluationCorpus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		wantErr string
	}{
		{name: "valid", input: "{\"version\":1,\"scenarios\":[{\"id\":\"one\",\"language\":\"en\",\"windows\":[{\"articles\":[],\"extraction_json\":[]}],\"expected_memories\":[],\"prohibited_fragments\":[],\"superseded_fragments\":[],\"retrievals\":[]}] }"},
		{name: "unknown field", input: "{\"version\":1,\"scenarios\":[],\"extra\":true}", wantErr: "unknown field"},
		{name: "missing scenarios", input: "{\"version\":1,\"scenarios\":[]}", wantErr: "version and scenarios are required"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			corpus, err := memory.LoadEvaluationCorpus(strings.NewReader(test.input))
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("LoadEvaluationCorpus() error = %v, want containing %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadEvaluationCorpus() error = %v", err)
			}
			if corpus.Version != 1 || len(corpus.Scenarios) != 1 {
				t.Fatalf("LoadEvaluationCorpus() = %#v", corpus)
			}
		})
	}
}

func TestRunEvaluationProductionPaths(t *testing.T) {
	t.Parallel()

	file, err := os.Open("testdata/evaluation/corpus.json")
	if err != nil {
		t.Fatalf("open corpus: %v", err)
	}
	corpus, err := memory.LoadEvaluationCorpus(file)
	if err != nil {
		t.Fatalf("LoadEvaluationCorpus() error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close corpus: %v", err)
	}
	report, err := memory.RunEvaluation(context.Background(), corpus, memory.EvaluationOptions{
		RetrievalPlanningEnabled: true,
		StoreFactory: func() ai.SemanticStore {
			return semanticstore.New()
		},
	})
	if err != nil {
		t.Fatalf("RunEvaluation() error = %v", err)
	}
	if report.Metrics.FormationF1 != 1 || report.Metrics.SupersessionCorrectness != 1 {
		t.Fatalf("formation metrics = %#v", report.Metrics)
	}
	if report.Metrics.NDCGAt5 != 1 || report.Metrics.PlanningCallCount != report.Metrics.RetrievalCount {
		t.Fatalf("retrieval metrics = %#v", report.Metrics)
	}
}

func TestCompareEvaluationReports(t *testing.T) {
	t.Parallel()

	baseline := memory.EvaluationReport{Metrics: memory.EvaluationMetrics{
		FormationF1: 1, NDCGAt5: 1, ExtractionCallCount: 10, PlanningCallCount: 10,
	}}
	tests := []struct {
		name      string
		candidate memory.EvaluationReport
		accepted  bool
	}{
		{name: "accepted simplification", candidate: memory.EvaluationReport{Metrics: memory.EvaluationMetrics{FormationF1: 0.99, NDCGAt5: 0.99, ExtractionCallCount: 10, PlanningCallCount: 6}}, accepted: true},
		{name: "quality regression", candidate: memory.EvaluationReport{Metrics: memory.EvaluationMetrics{FormationF1: 0.97, NDCGAt5: 1, ExtractionCallCount: 10, PlanningCallCount: 6}}},
		{name: "no cost reduction", candidate: memory.EvaluationReport{Metrics: memory.EvaluationMetrics{FormationF1: 1, NDCGAt5: 1, ExtractionCallCount: 10, PlanningCallCount: 10}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			comparison := memory.CompareEvaluationReports(baseline, test.candidate, test.name == "no cost reduction")
			if comparison.Accepted != test.accepted {
				t.Fatalf("CompareEvaluationReports().Accepted = %t, want %t; violations=%v", comparison.Accepted, test.accepted, comparison.Violations)
			}
		})
	}
}

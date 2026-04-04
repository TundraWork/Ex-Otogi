package managementhttp

import (
	"testing"
)

func TestGenerateOpenAPIYAML(t *testing.T) {
	t.Parallel()

	yaml, err := GenerateOpenAPIYAML()
	if err != nil {
		t.Fatalf("GenerateOpenAPIYAML failed: %v", err)
	}
	if len(yaml) == 0 {
		t.Fatal("GenerateOpenAPIYAML returned empty YAML")
	}

	expectedPaths := []string{
		"/panel/events",
		"/panel/overview",
		"/panel/traces/{trace_id}",
		"/panel/artifacts/{id}",
		"/panel/snapshots",
	}
	for _, path := range expectedPaths {
		if !containsString(string(yaml), path) {
			t.Errorf("generated YAML missing path %q", path)
		}
	}
}

func containsString(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

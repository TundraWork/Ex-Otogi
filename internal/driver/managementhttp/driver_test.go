package managementhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	kernelmanagement "ex-otogi/internal/kernel/management"
	panel "ex-otogi/pkg/otogi/management"
)

func TestHandlerRejectsMissingOrInvalidToken(t *testing.T) {
	t.Parallel()

	service := seedService(t)
	handler, err := newHandler(service, "secret")
	if err != nil {
		t.Fatalf("newHandler failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/panel/events", nil)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("missing token status = %d, want 401", resp.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/panel/events", nil)
	req.Header.Set("Authorization", "Bearer nope")
	resp = httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("invalid token status = %d, want 401", resp.Code)
	}
}

func TestHandlerAuthorizedQueriesAndEventListing(t *testing.T) {
	t.Parallel()

	service := seedService(t)
	handler, err := newHandler(service, "secret")
	if err != nil {
		t.Fatalf("newHandler failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/panel/events?after_id=1", nil)
	req.Header.Set("Authorization", "Bearer secret")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.Code)
	}

	var body panel.EventPage
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body failed: %v", err)
	}
	if body.CursorResetRequired {
		t.Fatal("CursorResetRequired = true, want false (no retention eviction)")
	}
	if len(body.Items) == 0 {
		t.Fatal("expected retained events in response")
	}
}

func TestHandlerReturnsNotFoundForMissingEventAndArtifact(t *testing.T) {
	t.Parallel()

	service := seedService(t)
	handler, err := newHandler(service, "secret")
	if err != nil {
		t.Fatalf("newHandler failed: %v", err)
	}

	for _, path := range []string{"/panel/events/999", "/panel/artifacts/missing"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer secret")
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, req)
		if resp.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404", path, resp.Code)
		}
	}
}

func TestNormalizeListenAddressDefaultsToLoopback(t *testing.T) {
	t.Parallel()

	if got := normalizeListenAddress(""); got != "127.0.0.1:8080" {
		t.Fatalf("default listen address = %q, want 127.0.0.1:8080", got)
	}
	if got := normalizeListenAddress("127.0.0.1"); got != "127.0.0.1:8080" {
		t.Fatalf("host-only listen address = %q, want 127.0.0.1:8080", got)
	}
}

func TestDriverShutdownIsClean(t *testing.T) {
	t.Parallel()

	driver, err := New(seedService(t), Config{
		ListenAddress: "127.0.0.1:0",
		BearerToken:   "secret",
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- driver.Start(ctx, nil)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Start returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("driver shutdown timed out")
	}
}

func seedService(t *testing.T) *kernelmanagement.SQLiteStore {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "management.db")
	service, err := kernelmanagement.NewSQLiteStore(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}
	t.Cleanup(func() { service.Close() })

	ctx := context.Background()
	first, err := service.RecordEvent(ctx, panel.Event{
		Category: panel.EventCategoryRuntime,
		Kind:     "runtime.one",
		Level:    panel.EventLevelInfo,
	})
	if err != nil {
		t.Fatalf("RecordEvent(first) failed: %v", err)
	}
	second, err := service.RecordEvent(ctx, panel.Event{
		Category: panel.EventCategoryRuntime,
		Kind:     "runtime.two",
		Level:    panel.EventLevelInfo,
	})
	if err != nil {
		t.Fatalf("RecordEvent(second) failed: %v", err)
	}
	third, err := service.RecordEvent(ctx, panel.Event{
		Category: panel.EventCategoryRuntime,
		Kind:     "runtime.three",
		Level:    panel.EventLevelInfo,
	})
	if err != nil {
		t.Fatalf("RecordEvent(third) failed: %v", err)
	}
	if _, err := service.RecordArtifact(ctx, panel.Artifact{
		ID:      "art-1",
		EventID: second.ID,
		Kind:    panel.ArtifactKindPromptFull,
		Content: "prompt",
	}); err != nil {
		t.Fatalf("RecordArtifact failed: %v", err)
	}
	_ = first
	_ = third

	return service
}

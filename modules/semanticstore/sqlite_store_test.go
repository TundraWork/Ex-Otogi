package semanticstore

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ex-otogi/pkg/otogi/ai"
)

func TestSQLiteStoreDurabilityAndFingerprint(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "memory.db")
	fingerprint := EmbeddingFingerprint{Provider: "main", Model: "embedding-v1", Dimensions: 2}
	scope := ai.SemanticScope{Platform: "telegram", ConversationID: "chat-1"}
	store, err := OpenSQLiteStore(ctx, path, fingerprint, func() time.Time { return time.Unix(100, 0) }, func() string { return "mem-1" })
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	record, err := store.Store(ctx, ai.SemanticEntry{Scope: scope, Content: "Alice likes tea", Category: "preference", Embedding: []float32{1, 0}, Profile: ai.SemanticProfile{Kind: ai.SemanticKindUnit, Importance: 8}, Keywords: []string{"tea"}})
	if err != nil {
		t.Fatalf("store record: %v", err)
	}
	if record.ID != "mem-1" {
		t.Fatalf("record id = %q", record.ID)
	}
	if err := store.Close(ctx); err != nil {
		t.Fatalf("close store: %v", err)
	}

	reopened, err := OpenSQLiteStore(ctx, path, fingerprint, nil, nil)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer func() { _ = reopened.Close(context.Background()) }()
	matches, err := reopened.Search(ctx, ai.SemanticQuery{Scope: scope, Embedding: []float32{1, 0}, Limit: 5, MinSimilarity: 0.1})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(matches) != 1 || matches[0].Record.Content != "Alice likes tea" {
		t.Fatalf("matches = %+v", matches)
	}
	if len(matches[0].Record.Keywords) != 1 || matches[0].Record.Keywords[0] != "tea" {
		t.Fatalf("keywords = %v", matches[0].Record.Keywords)
	}

	_, err = reopened.Store(ctx, ai.SemanticEntry{Scope: scope, Content: "wrong vector", Category: "knowledge", Embedding: []float32{1}, Profile: ai.SemanticProfile{Kind: ai.SemanticKindUnit}})
	if err == nil || !strings.Contains(err.Error(), "dimensions") {
		t.Fatalf("dimension error = %v", err)
	}
	if err := reopened.Close(ctx); err != nil {
		t.Fatalf("close reopened: %v", err)
	}
	reopened = nil
	_, err = OpenSQLiteStore(ctx, path, EmbeddingFingerprint{Provider: "main", Model: "embedding-v2", Dimensions: 2}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "fingerprint mismatch") {
		t.Fatalf("fingerprint error = %v", err)
	}
}

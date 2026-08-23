package management

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"

	panel "ex-otogi/pkg/otogi/management"
)

func BenchmarkSQLiteListEvents100K(b *testing.B) {
	ctx := context.Background()
	store, err := NewSQLiteStore(ctx, filepath.Join(b.TempDir(), "management.db"))
	if err != nil {
		b.Fatalf("NewSQLiteStore: %v", err)
	}
	b.Cleanup(func() { _ = store.Close() })
	seedBenchmarkEvents(ctx, b, store, 100_000)

	queries := []struct {
		name  string
		query panel.EventQuery
	}{
		{name: "latest", query: panel.EventQuery{Limit: 50}},
		{name: "newer", query: panel.EventQuery{AfterID: 50_000, Limit: 50}},
		{name: "older", query: panel.EventQuery{BeforeID: 50_000, Limit: 50}},
		{name: "filtered_latest", query: panel.EventQuery{Category: panel.EventCategoryLLM, Module: "llmchat", Limit: 50}},
	}
	for _, benchmark := range queries {
		b.Run(benchmark.name, func(b *testing.B) {
			durations := make([]time.Duration, 0, b.N)
			b.ResetTimer()
			for range b.N {
				startedAt := time.Now()
				if _, err := store.ListEvents(ctx, benchmark.query); err != nil {
					b.Fatalf("ListEvents: %v", err)
				}
				durations = append(durations, time.Since(startedAt))
			}
			b.StopTimer()
			slices.Sort(durations)
			if len(durations) > 0 {
				p95Index := (len(durations)*95 - 1) / 100
				b.ReportMetric(float64(durations[p95Index].Nanoseconds()), "p95-ns/op")
			}
		})
	}
}

func seedBenchmarkEvents(ctx context.Context, b *testing.B, store *SQLiteStore, count int) {
	b.Helper()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		b.Fatalf("begin seed transaction: %v", err)
	}
	statement, err := tx.PrepareContext(ctx, `
		INSERT INTO events (occurred_at, category, kind, level, module, subject)
		VALUES (?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		if rollbackErr := tx.Rollback(); rollbackErr != nil {
			b.Fatalf("prepare seed insert: %v; rollback: %v", err, rollbackErr)
		}
		b.Fatalf("prepare seed insert: %v", err)
	}
	defer statement.Close()

	for index := range count {
		category := panel.EventCategoryRuntime
		module := "kernel"
		if index%10 == 0 {
			category = panel.EventCategoryLLM
			module = "llmchat"
		}
		if _, err := statement.ExecContext(
			ctx, time.Unix(int64(index), 0).UTC(), category, fmt.Sprintf("event.%d", index%20),
			panel.EventLevelInfo, module, "benchmark event",
		); err != nil {
			if rollbackErr := tx.Rollback(); rollbackErr != nil {
				b.Fatalf("insert seed event %d: %v; rollback: %v", index, err, rollbackErr)
			}
			b.Fatalf("insert seed event %d: %v", index, err)
		}
	}
	if err := tx.Commit(); err != nil {
		b.Fatalf("commit seed events: %v", err)
	}
}

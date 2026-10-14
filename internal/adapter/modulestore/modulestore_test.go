package modulestore

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/TundraWork/Ex-Otogi/pkg/otogi"
)

func TestStoreIsolationAndPrefixes(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	a, b := s.Store("a"), s.Store("b")
	for _, key := range []string{"笔记/1", "笔记/2", "other"} {
		if err := a.Put(ctx, key, []byte(key)); err != nil {
			t.Fatal(err)
		}
	}
	if _, found, _ := b.Get(ctx, "other"); found {
		t.Fatal("module b sees module a's key")
	}
	if keys, err := a.List(ctx, "笔记/"); err != nil || !slices.Equal(keys, []string{"笔记/1", "笔记/2"}) {
		t.Fatalf("List = %v, %v", keys, err)
	}
	if err := a.Delete(ctx, "other"); err != nil {
		t.Fatal(err)
	}
	if err := a.Delete(ctx, "missing"); err != nil {
		t.Fatalf("deleting a missing key: %v", err)
	}
	db, err := s.Database(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "CREATE TABLE t (x)"); err != nil {
		t.Fatal(err)
	}
	if again, _ := s.Database(ctx, "a"); again != db {
		t.Fatal("Database opened twice")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	if v, found, err := reopened.Store("a").Get(ctx, "笔记/1"); err != nil || !found || string(v) != "笔记/1" {
		t.Fatalf("after reopen = %q, %v, %v", v, found, err)
	}
}

func TestTimers(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	base := time.Unix(1000, 0)
	due := func(now time.Time) []string {
		var keys []string
		if err := s.DueTimers(ctx, now, []otogi.ModuleID{"m"}, func(m otogi.ModuleID, tm otogi.Timer) {
			keys = append(keys, string(m)+"/"+tm.Key+"/"+string(tm.Payload))
		}); err != nil {
			t.Fatal(err)
		}
		return keys
	}
	_ = s.SetTimer(ctx, "m", "a", base, []byte("1"))
	_ = s.SetTimer(ctx, "m", "b", base.Add(time.Hour), nil)
	_ = s.SetTimer(ctx, "disabled", "x", base, nil) // a module not in the list
	if got := due(base); !slices.Equal(got, []string{"m/a/1"}) {
		t.Fatalf("due = %v", got)
	}
	// A failed run is retried later but keeps its due time.
	_ = s.RetryTimer(ctx, "m", "a", base, base.Add(time.Minute))
	if got := due(base.Add(30 * time.Second)); len(got) != 0 {
		t.Fatalf("due before retry = %v", got)
	}
	// Replacing a timer while it runs: finishing the old run keeps the new one.
	_ = s.SetTimer(ctx, "m", "a", base.Add(2*time.Minute), []byte("2"))
	_ = s.FinishTimer(ctx, "m", "a", base)
	if got := due(base.Add(2 * time.Minute)); !slices.Equal(got, []string{"m/a/2"}) {
		t.Fatalf("after replace = %v", got)
	}
	_ = s.FinishTimer(ctx, "m", "a", base.Add(2*time.Minute))
	_ = s.CancelTimer(ctx, "m", "b")
	if got := due(base.Add(24 * time.Hour)); len(got) != 0 {
		t.Fatalf("after finish and cancel = %v", got)
	}
}

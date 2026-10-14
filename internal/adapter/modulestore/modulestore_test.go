package modulestore

import (
	"context"
	"slices"
	"testing"
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

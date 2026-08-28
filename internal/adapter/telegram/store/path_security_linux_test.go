//go:build linux

package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenRejectsUnsafeDatabaseParent(t *testing.T) {
	t.Run("mode", func(t *testing.T) {
		dir := privateStoreDirectory(t)
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "telegram.db")
		if _, err := Open(context.Background(), testConfig(path)); !errors.Is(err, ErrInvalidPath) {
			t.Fatalf("Open() error = %v, want ErrInvalidPath", err)
		}
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("database created in unsafe parent: %v", err)
		}
	})

	t.Run("symlink", func(t *testing.T) {
		root := privateStoreDirectory(t)
		realParent := filepath.Join(root, "real")
		if err := os.Mkdir(realParent, 0o700); err != nil {
			t.Fatal(err)
		}
		linkParent := filepath.Join(root, "link")
		if err := os.Symlink(realParent, linkParent); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(linkParent, "telegram.db")
		if _, err := Open(context.Background(), testConfig(path)); !errors.Is(err, ErrInvalidPath) {
			t.Fatalf("Open() error = %v, want ErrInvalidPath", err)
		}
		if _, err := os.Lstat(filepath.Join(realParent, "telegram.db")); !os.IsNotExist(err) {
			t.Fatalf("database created through symlink parent: %v", err)
		}
	})
}

func TestOpenRejectsHardlinkedDatabase(t *testing.T) {
	dir := privateStoreDirectory(t)
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "telegram.db")
	if err := os.Link(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(context.Background(), testConfig(path)); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("Open() error = %v, want ErrInvalidPath", err)
	}
}

func TestOpenRejectsUnsafeExistingSQLiteSidecars(t *testing.T) {
	for _, suffix := range []string{"-wal", "-shm"} {
		t.Run(suffix, func(t *testing.T) {
			dir := privateStoreDirectory(t)
			path := filepath.Join(dir, "telegram.db")
			if err := os.WriteFile(path+suffix, nil, 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := Open(context.Background(), testConfig(path)); !errors.Is(err, ErrInvalidPath) {
				t.Fatalf("Open() error = %v, want ErrInvalidPath", err)
			}
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Fatalf("database created beside unsafe sidecar: %v", err)
			}
		})
	}
}

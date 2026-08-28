package telegram

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestNewSessionStorageUsesGotdFileStorage(t *testing.T) {
	dir := privateDirectory(t)
	path := filepath.Join(dir, "telegram.session")

	storage, err := NewSessionStorage(path)
	if err != nil {
		t.Fatalf("NewSessionStorage() error = %v", err)
	}
	if storage.Path != path {
		t.Fatalf("Path = %q, want %q", storage.Path, path)
	}
}

func TestValidateSessionPathRejectsUnsafeModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode contract")
	}

	t.Run("parent", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		err := ValidateSessionPath(filepath.Join(dir, "telegram.session"))
		if err == nil {
			t.Fatal("ValidateSessionPath() accepted mode 0755 parent")
		}
	})

	t.Run("file", func(t *testing.T) {
		dir := privateDirectory(t)
		path := filepath.Join(dir, "telegram.session")
		if err := os.WriteFile(path, []byte("opaque"), 0o644); err != nil {
			t.Fatal(err)
		}
		err := ValidateSessionPath(path)
		if err == nil {
			t.Fatal("ValidateSessionPath() accepted mode 0644 file")
		}
	})
}

func TestValidateSessionPathAcceptsMissingOrPrivateFile(t *testing.T) {
	dir := privateDirectory(t)
	path := filepath.Join(dir, "telegram.session")
	if err := ValidateSessionPath(path); err != nil {
		t.Fatalf("missing file: %v", err)
	}
	if err := os.WriteFile(path, []byte("opaque"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateSessionPath(path); err != nil {
		t.Fatalf("private file: %v", err)
	}
}

func TestValidateSessionPathRejectsSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink setup differs on Windows")
	}
	dir := privateDirectory(t)
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("opaque"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "telegram.session")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := ValidateSessionPath(link); err == nil {
		t.Fatal("ValidateSessionPath() accepted symlink")
	}
}

func TestValidateSessionPathRejectsSymlinkParent(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux ownership and link contract")
	}
	root := privateDirectory(t)
	realParent := filepath.Join(root, "real")
	if err := os.Mkdir(realParent, 0o700); err != nil {
		t.Fatal(err)
	}
	linkParent := filepath.Join(root, "link")
	if err := os.Symlink(realParent, linkParent); err != nil {
		t.Fatal(err)
	}
	if err := ValidateSessionPath(filepath.Join(linkParent, "telegram.session")); err == nil {
		t.Fatal("ValidateSessionPath() accepted symlink parent")
	}
}

func privateDirectory(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

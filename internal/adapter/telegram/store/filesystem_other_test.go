//go:build !linux

package store

import (
	"os"
	"testing"
)

func TestFilesystemChecksFailClosed(t *testing.T) {
	if err := validateLocalFilesystem(t.TempDir()); err == nil {
		t.Fatal("validateLocalFilesystem() accepted an unsupported platform")
	}
	info, err := os.Stat(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if ownedByCurrentUser(info) || hasSingleLink(info) {
		t.Fatal("unsupported platform positively verified ownership or link count")
	}
}

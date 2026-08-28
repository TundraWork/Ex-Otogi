//go:build !linux

package telegram

import "testing"

func TestValidateLocalFilesystemFailsClosed(t *testing.T) {
	if err := validateLocalFilesystem(t.TempDir()); err == nil {
		t.Fatal("validateLocalFilesystem() accepted an unsupported platform")
	}
}

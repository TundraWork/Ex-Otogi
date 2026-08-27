// Package telegram provides the Telegram adapter's storage boundaries.
package telegram

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	gotdsession "github.com/gotd/td/session"
)

// ValidateSessionPath verifies that path names a private regular file, or a
// missing file in a private local directory. Symlinks are rejected so the
// validated object cannot be redirected outside the owned directory.
func ValidateSessionPath(path string) error {
	if path == "" {
		return errors.New("validate Telegram session path: empty path")
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("validate Telegram session path: path must be absolute and clean")
	}
	absolute := path

	parent := filepath.Dir(absolute)
	parentInfo, err := os.Lstat(parent)
	if err != nil {
		return fmt.Errorf("validate Telegram session path: parent: %w", err)
	}
	if !parentInfo.IsDir() || parentInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("validate Telegram session path: parent must be a real directory")
	}
	if runtime.GOOS != "windows" && parentInfo.Mode().Perm() != 0o700 {
		return fmt.Errorf("validate Telegram session path: parent mode is %04o, want 0700", parentInfo.Mode().Perm())
	}
	if err := validateLocalFilesystem(parent); err != nil {
		return fmt.Errorf("validate Telegram session path: %w", err)
	}

	fileInfo, err := os.Lstat(absolute)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("validate Telegram session path: file: %w", err)
	}
	if !fileInfo.Mode().IsRegular() {
		return errors.New("validate Telegram session path: file must be regular")
	}
	if runtime.GOOS != "windows" && fileInfo.Mode().Perm() != 0o600 {
		return fmt.Errorf("validate Telegram session path: file mode is %04o, want 0600", fileInfo.Mode().Perm())
	}
	return nil
}

// NewSessionStorage validates path and returns gotd's top-level file-backed
// session storage. Session bytes remain opaque to the adapter.
func NewSessionStorage(path string) (*gotdsession.FileStorage, error) {
	if err := ValidateSessionPath(path); err != nil {
		return nil, err
	}
	return &gotdsession.FileStorage{Path: path}, nil
}

//go:build !linux

package store

import (
	"errors"
	"os"
)

func validateLocalFilesystem(string) error {
	return errors.New("filesystem locality cannot be positively verified on this platform")
}

func ownedByCurrentUser(os.FileInfo) bool { return false }

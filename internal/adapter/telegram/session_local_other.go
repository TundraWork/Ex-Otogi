//go:build !linux

package telegram

import (
	"errors"
	"os"
)

func validateLocalFilesystem(string) error {
	return errors.New("filesystem locality cannot be positively verified on this platform")
}

func validatePathOwnerAndLinks(os.FileInfo, bool) error {
	return errors.New("path ownership cannot be positively verified on this platform")
}
